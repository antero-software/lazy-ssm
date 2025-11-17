package tunnel

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/ec2"
	"github.com/antero-software/lazy-ssm/sso"
)

// LazySSMTunnel manages an on-demand SSM tunnel
type LazySSMTunnel struct {
	config         config.TunnelConfig
	appConfig      *config.Config
	tunnelPort     int
	tunnelCmd      *exec.Cmd
	mu             sync.Mutex
	lastActivity   atomic.Int64
	connections    atomic.Int32
	resolvedInstID string // Cached resolved instance ID from pattern
}

// NewLazySSMTunnel creates a new tunnel manager
func NewLazySSMTunnel(tunnelConfig config.TunnelConfig, appConfig *config.Config) *LazySSMTunnel {
	return &LazySSMTunnel{
		config:     tunnelConfig,
		appConfig:  appConfig,
		tunnelPort: tunnelConfig.LocalPort + appConfig.Network.TunnelPortOffset,
	}
}

// resolveInstanceID resolves the instance ID from either explicit ID or pattern
func (t *LazySSMTunnel) resolveInstanceID(ctx context.Context) (string, error) {
	// If we have an explicit instance ID, use it
	if t.config.InstanceID != "" {
		return t.config.InstanceID, nil
	}

	// If we have a cached resolved ID, use it
	if t.resolvedInstID != "" {
		return t.resolvedInstID, nil
	}

	// Resolve from pattern
	if t.config.InstancePattern == "" {
		return "", fmt.Errorf("neither instance_id nor instance_pattern specified")
	}

	// Determine which AWS profile/region to use
	profile := t.config.AWSProfile
	if profile == "" {
		profile = t.appConfig.AWS.Profile
	}
	region := t.config.AWSRegion
	if region == "" {
		region = t.appConfig.AWS.Region
	}

	log.Printf("[%s] Discovering instance matching pattern: %s", t.config.Description, t.config.InstancePattern)

	discovery := ec2.NewInstanceDiscovery(t.appConfig.AWS.CLIPath, profile, region)
	instanceID, err := discovery.FindInstanceWithSSM(ctx, t.config.InstancePattern)
	if err != nil {
		return "", fmt.Errorf("failed to discover instance: %w", err)
	}

	log.Printf("[%s] Discovered instance: %s", t.config.Description, instanceID)

	// Cache the resolved ID
	t.resolvedInstID = instanceID
	return instanceID, nil
}

// ensureTunnel starts the SSM tunnel if not running
func (t *LazySSMTunnel) ensureTunnel() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check if tunnel is already running
	if t.tunnelCmd != nil && t.tunnelCmd.Process != nil {
		// Check if process is still alive
		if err := t.tunnelCmd.Process.Signal(os.Signal(nil)); err == nil {
			return nil // Tunnel is still running
		}
	}

	log.Printf("[%s] Starting SSM tunnel on port %d...", t.config.Description, t.tunnelPort)

	// Determine which AWS profile/region to use for SSO check
	profile := t.config.AWSProfile
	if profile == "" {
		profile = t.appConfig.AWS.Profile
	}
	region := t.config.AWSRegion
	if region == "" {
		region = t.appConfig.AWS.Region
	}

	// Check SSO authentication before proceeding
	ctx := context.Background()
	if err := sso.CheckProfile(ctx, t.appConfig.AWS.CLIPath, profile, region); err != nil {
		return fmt.Errorf("SSO authentication failed: %w", err)
	}

	// Resolve instance ID (from explicit ID or pattern)
	instanceID, err := t.resolveInstanceID(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve instance ID: %w", err)
	}

	// Build AWS CLI command
	args := []string{"ssm", "start-session",
		"--target", instanceID,
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters", fmt.Sprintf("host=%s,portNumber=%d,localPortNumber=%d",
			t.config.RDSEndpoint, t.config.RDSPort, t.tunnelPort),
	}

	// Add profile if specified (already determined above)
	if profile != "" {
		args = append([]string{"--profile", profile}, args...)
	}

	// Add region if specified (already determined above)
	if region != "" {
		args = append([]string{"--region", region}, args...)
	}

	// Start new SSM session
	t.tunnelCmd = exec.Command(t.appConfig.AWS.CLIPath, args...)

	// Capture output for debugging
	t.tunnelCmd.Stdout = log.Writer()
	t.tunnelCmd.Stderr = log.Writer()

	if err := t.tunnelCmd.Start(); err != nil {
		return fmt.Errorf("failed to start SSM tunnel: %w", err)
	}

	// Wait for tunnel to be ready by checking port availability
	readyTimeout := t.appConfig.Timeouts.TunnelReady
	pollInterval := t.appConfig.Timeouts.TunnelReadyPoll
	startTime := time.Now()

	log.Printf("[%s] Waiting for SSM tunnel to become ready...", t.config.Description)

	for time.Since(startTime) < readyTimeout {
		// Check if port is listening by attempting to dial
		// We close immediately to minimize impact, but this tells us the tunnel is ready
		conn, err := net.DialTimeout("tcp",
			fmt.Sprintf("%s:%d", t.appConfig.Network.ListenAddress, t.tunnelPort),
			t.appConfig.Timeouts.Connection)

		if err == nil {
			conn.Close()
			log.Printf("[%s] Tunnel ready on port %d", t.config.Description, t.tunnelPort)
			// Small delay to let the connection fully close before client connects
			time.Sleep(200 * time.Millisecond)
			return nil
		}

		// Not ready yet, wait and try again
		time.Sleep(pollInterval)
	}

	// If we got here, timeout occurred - check if process actually died
	if t.tunnelCmd.Process != nil {
		// Try to signal the process to see if it's still alive
		if err := t.tunnelCmd.Process.Signal(os.Signal(nil)); err != nil {
			return fmt.Errorf("SSM tunnel process died during startup")
		}
	}

	return fmt.Errorf("tunnel failed to become ready after %v (process still running but port not accepting connections)", readyTimeout)
}

// handleConnection handles a client connection
func (t *LazySSMTunnel) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	t.connections.Add(1)
	defer t.connections.Add(-1)

	clientAddr := clientConn.RemoteAddr().String()
	log.Printf("[%s] New connection from %s", t.config.Description, clientAddr)

	// Ensure tunnel is running
	if err := t.ensureTunnel(); err != nil {
		log.Printf("[%s] Failed to ensure tunnel: %v", t.config.Description, err)
		return
	}

	// Connect to the actual SSM tunnel
	tunnelConn, err := net.Dial("tcp",
		fmt.Sprintf("%s:%d", t.appConfig.Network.ListenAddress, t.tunnelPort))
	if err != nil {
		log.Printf("[%s] Failed to connect to tunnel: %v", t.config.Description, err)
		return
	}
	defer tunnelConn.Close()

	// Update activity timestamp
	t.lastActivity.Store(time.Now().Unix())

	// Create error channel for goroutines
	errCh := make(chan error, 2)

	// Proxy data bidirectionally
	go t.copyData(tunnelConn, clientConn, errCh, "client->RDS")
	go t.copyData(clientConn, tunnelConn, errCh, "RDS->client")

	// Wait for either direction to close
	err = <-errCh
	if err != nil && err != io.EOF {
		log.Printf("[%s] Connection error: %v", t.config.Description, err)
	}

	log.Printf("[%s] Connection from %s closed", t.config.Description, clientAddr)
}

// copyData copies data between connections
func (t *LazySSMTunnel) copyData(dst, src net.Conn, errCh chan<- error, direction string) {
	_, err := io.Copy(dst, src)
	errCh <- err

	// Update activity on each data transfer
	t.lastActivity.Store(time.Now().Unix())
}

// Start begins listening for connections
func (t *LazySSMTunnel) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp",
		fmt.Sprintf("%s:%d", t.appConfig.Network.ListenAddress, t.config.LocalPort))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", t.config.LocalPort, err)
	}
	defer listener.Close()

	log.Printf("[%s] Listening on port %d", t.config.Description, t.config.LocalPort)

	// Start idle monitor
	go t.monitorIdle(ctx)

	// Accept connections
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				log.Printf("[%s] Accept error: %v", t.config.Description, err)
				continue
			}
		}

		go t.handleConnection(conn)
	}
}

// monitorIdle closes the tunnel after inactivity
func (t *LazySSMTunnel) monitorIdle(ctx context.Context) {
	ticker := time.NewTicker(t.appConfig.Timeouts.IdleCheck)
	defer ticker.Stop()

	idleTimeout := int64(t.appConfig.Timeouts.IdleTimeout.Seconds())

	for {
		select {
		case <-ctx.Done():
			t.shutdown()
			return
		case <-ticker.C:
			lastActivity := t.lastActivity.Load()
			connections := t.connections.Load()

			if connections == 0 && lastActivity > 0 {
				idleTime := time.Now().Unix() - lastActivity
				if idleTime > idleTimeout {
					log.Printf("[%s] Idle timeout reached, closing tunnel", t.config.Description)
					t.closeTunnel()
					t.lastActivity.Store(0)
				}
			}
		}
	}
}

// closeTunnel terminates the SSM tunnel
func (t *LazySSMTunnel) closeTunnel() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.tunnelCmd != nil && t.tunnelCmd.Process != nil {
		log.Printf("[%s] Terminating SSM tunnel", t.config.Description)
		t.tunnelCmd.Process.Kill()
		t.tunnelCmd.Wait()
		t.tunnelCmd = nil
	}
}

// shutdown gracefully shuts down the tunnel
func (t *LazySSMTunnel) shutdown() {
	log.Printf("[%s] Shutting down", t.config.Description)

	// Wait for active connections to finish (with timeout)
	timeout := time.After(t.appConfig.Timeouts.Shutdown)
	ticker := time.NewTicker(t.appConfig.Timeouts.ShutdownPoll)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			log.Printf("[%s] Shutdown timeout, forcing close", t.config.Description)
			t.closeTunnel()
			return
		case <-ticker.C:
			if t.connections.Load() == 0 {
				t.closeTunnel()
				return
			}
		}
	}
}
