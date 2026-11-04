package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/diagnose"
	"github.com/antero-software/lazy-ssm/ec2"
	"github.com/antero-software/lazy-ssm/notify"
	"github.com/antero-software/lazy-ssm/sso"
)

const (
	maxPortAttempts = 100 // Maximum number of ports to try
)

// LazySSMTunnel manages an on-demand SSM tunnel
type LazySSMTunnel struct {
	config          config.TunnelConfig
	appConfig       *config.Config
	tunnelPort      int
	tunnelCmd       *exec.Cmd
	mu              sync.Mutex
	lastActivity    atomic.Int64
	connections     atomic.Int32
	tunnelStartedAt atomic.Int64  // Unix seconds when the current SSM process started (0 if not running)
	resolvedInstID  string        // Cached resolved instance ID from pattern
	tunnelExit      chan struct{} // Closed when tunnelCmd exits (guarded by mu)
	stoppedCmd      *exec.Cmd     // Process that closeTunnel is stopping on purpose (guarded by mu)

	// problem shows a pop-up the user must act on (notify.Problem in production).
	problem func(tunnel, title, message string)

	// runDiagnosis runs the AWS checks (defaultDiagnosis in production).
	runDiagnosis  func(ctx context.Context) diagnose.Report
	diagnosing    atomic.Bool
	lastDiagnosis atomic.Int64 // Unix nanoseconds when the last diagnosis finished
	diagWG        sync.WaitGroup
	diagMu        sync.Mutex
	strongCause   error // a clear failure seen while a quiet one was being checked (guarded by diagMu)
}

// processWaitDelay bounds how long Wait waits for output pipes after the SSM
// process exits (a child such as session-manager-plugin can keep them open).
const processWaitDelay = 2 * time.Second

// findFreePort attempts to find a free port starting from the specified port
func findFreePort(listenAddr string, startPort int) (int, error) {
	for i := 0; i < maxPortAttempts; i++ {
		port := startPort + i
		addr := net.JoinHostPort(listenAddr, strconv.Itoa(port))

		// Try to listen on the port
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			// Port is free, close the listener and return the port
			listener.Close()
			return port, nil
		}
	}

	return 0, fmt.Errorf("could not find free port in range %d-%d", startPort, startPort+maxPortAttempts-1)
}

// NewLazySSMTunnel creates a new tunnel manager
func NewLazySSMTunnel(tunnelConfig config.TunnelConfig, appConfig *config.Config) *LazySSMTunnel {
	t := &LazySSMTunnel{
		config:     tunnelConfig,
		appConfig:  appConfig,
		problem:    notify.Problem,
		tunnelPort: tunnelConfig.LocalPort + appConfig.Network.TunnelPortOffset,
	}
	t.runDiagnosis = t.defaultDiagnosis
	return t
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

	profile := t.appConfig.ProfileFor(t.config)
	region := t.appConfig.RegionFor(t.config)

	log.Printf("[%s] Discovering instance matching pattern: %s", t.config.Description, t.config.InstancePattern)

	discovery := ec2.NewInstanceDiscovery(t.appConfig.AWS.CLIPath, profile, region)
	candidates, err := discovery.ListCandidates(ctx, t.config.InstancePattern)
	if err != nil {
		return "", fmt.Errorf("failed to discover instance: %w", err)
	}
	chosen, eligible, ok := ec2.PickSSMCandidate(candidates)
	if !ok {
		return "", fmt.Errorf("failed to discover instance: no running SSM-managed instance found matching pattern: %s", t.config.InstancePattern)
	}
	if eligible > 1 && ec2.SpansVPCs(candidates) {
		log.Printf("[%s] WARNING: %d instances in different VPCs match '%s': %s. Using %s (%s).",
			t.config.Description, eligible, t.config.InstancePattern, ec2.FormatEligible(candidates), chosen.Name, chosen.ID)
	}
	instanceID := chosen.ID

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
		// Check if process is still alive. Signal 0 performs an existence
		// check without actually sending a signal: nil error means alive.
		if err := t.tunnelCmd.Process.Signal(syscall.Signal(0)); err == nil {
			return nil // Tunnel is still running
		}
	}

	// Find a free port for the tunnel, starting from the preferred port
	freePort, err := findFreePort(t.appConfig.Network.ListenAddress, t.tunnelPort)
	if err != nil {
		return fmt.Errorf("failed to find free port: %w", err)
	}

	// Update tunnel port if it changed
	if freePort != t.tunnelPort {
		log.Printf("[%s] Port %d in use, using port %d instead", t.config.Description, t.tunnelPort, freePort)
		t.tunnelPort = freePort
	}

	log.Printf("[%s] Starting SSM tunnel on port %d...", t.config.Description, t.tunnelPort)

	// Determine which AWS profile/region to use for SSO check
	profile := t.appConfig.ProfileFor(t.config)
	region := t.appConfig.RegionFor(t.config)

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
	if err := t.startProcess(exec.Command(t.appConfig.AWS.CLIPath, args...)); err != nil {
		return fmt.Errorf("failed to start SSM tunnel: %w", err)
	}
	cmd := t.tunnelCmd
	exitCh := t.tunnelExit

	// Wait for tunnel to be ready by checking port availability
	readyTimeout := t.appConfig.Timeouts.TunnelReady
	pollInterval := t.appConfig.Timeouts.TunnelReadyPoll
	startTime := time.Now()

	log.Printf("[%s] Waiting for SSM tunnel to become ready...", t.config.Description)

	for time.Since(startTime) < readyTimeout {
		// Check for early process exit before polling the port
		select {
		case <-exitCh:
			t.tunnelCmd = nil
			t.tunnelStartedAt.Store(0)
			return fmt.Errorf("SSM tunnel process exited during startup - check that instance matching '%s' exists and can reach %s:%d",
				t.config.InstancePattern, t.config.RDSEndpoint, t.config.RDSPort)
		default:
		}

		conn, err := net.DialTimeout("tcp",
			net.JoinHostPort(t.appConfig.Network.ListenAddress, strconv.Itoa(t.tunnelPort)),
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

	// Timeout - kill the hung process; the exit goroutine will log and clean up
	killProcessGroup(cmd)
	t.tunnelCmd = nil
	t.tunnelStartedAt.Store(0)

	return fmt.Errorf("tunnel failed to become ready after %v - verify instance matching '%s' can reach %s:%d",
		readyTimeout, t.config.InstancePattern, t.config.RDSEndpoint, t.config.RDSPort)
}

// displayName is the tunnel's description, or its name when it has none.
func (t *LazySSMTunnel) displayName() string {
	if t.config.Description != "" {
		return t.config.Description
	}
	return t.config.Name
}

// startProcess starts the SSM process, records its start time, and watches
// its output and exit. The caller holds t.mu.
func (t *LazySSMTunnel) startProcess(cmd *exec.Cmd) error {
	// Both output streams go through one pipe so every line is logged with
	// the tunnel name. exec copies into it, so Wait finishes the copy before
	// the watcher closes the pipe.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	cmd.WaitDelay = processWaitDelay
	// Own process group, so stopping the tunnel also stops the
	// session-manager-plugin that aws starts (and its SSM session).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return err
	}

	// Record when this SSM process started; the keepalive window is measured
	// from here (i.e. from the first connection that started the session).
	t.tunnelStartedAt.Store(time.Now().Unix())
	t.tunnelCmd = cmd
	exitCh := make(chan struct{})
	t.tunnelExit = exitCh

	go t.scanTunnelOutput(pr)

	// Watch for exit; clean up state and log. Never pops up: a failed
	// connection is reported where it fails (handleConnection).
	go func() {
		waitErr := cmd.Wait()
		pw.Close()
		close(exitCh)

		t.mu.Lock()
		intentional := t.stoppedCmd == cmd
		if intentional {
			t.stoppedCmd = nil
		}
		if t.tunnelCmd == cmd {
			t.tunnelCmd = nil
			t.tunnelStartedAt.Store(0)
			t.resolvedInstID = "" // Re-discover instance on next attempt
		}
		t.mu.Unlock()

		switch {
		case intentional:
			log.Printf("[%s] SSM tunnel stopped", t.config.Description)
		case waitErr != nil:
			log.Printf("[%s] ERROR: SSM tunnel process exited: %v", t.config.Description, waitErr)
			log.Printf("[%s] Hint: verify instance matching '%s' can reach %s:%d",
				t.config.Description, t.config.InstancePattern, t.config.RDSEndpoint, t.config.RDSPort)
		default:
			log.Printf("[%s] SSM tunnel process exited", t.config.Description)
		}
	}()
	return nil
}

// killProcessGroup kills the SSM process and every child it started.
func killProcessGroup(cmd *exec.Cmd) {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		cmd.Process.Kill()
	}
}

// scanTunnelOutput reads the SSM process output, logging each line and highlighting
// lines that contain error patterns so they're easy to spot in logs.
func (t *LazySSMTunnel) scanTunnelOutput(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") ||
			strings.Contains(lower, "unable") || strings.Contains(lower, "cannot") {
			log.Printf("[%s] SSM ERROR: %s", t.config.Description, line)
			if isDestinationFailure(line) {
				t.reportFailure(errDestination, triggerPlugin)
			}
		} else {
			log.Printf("[%s] SSM: %s", t.config.Description, line)
		}
	}
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
		t.reportFailure(err, triggerStart)
		return
	}

	// Connect to the actual SSM tunnel
	tunnelConn, err := net.Dial("tcp",
		net.JoinHostPort(t.appConfig.Network.ListenAddress, strconv.Itoa(t.tunnelPort)))
	if err != nil {
		log.Printf("[%s] Failed to connect to tunnel: %v", t.config.Description, err)
		return
	}
	defer tunnelConn.Close()

	// Update activity timestamp
	t.lastActivity.Store(time.Now().Unix())

	// Create error channel for goroutines
	errCh := make(chan error, 2)
	started := time.Now()
	var fromDB, fromClient atomic.Int64

	// Proxy data bidirectionally
	go t.copyData(tunnelConn, clientConn, errCh, &fromClient)
	go t.copyData(clientConn, tunnelConn, errCh, &fromDB)

	// Wait for either direction to close
	err = <-errCh
	if err != nil && err != io.EOF {
		log.Printf("[%s] Connection error: %v", t.config.Description, err)
	}

	log.Printf("[%s] Connection from %s closed", t.config.Description, clientAddr)
	if noResponse(fromDB.Load(), fromClient.Load(), time.Since(started)) {
		t.reportFailure(errNoResponse, triggerHeuristic)
	}
}

// copyData copies data between connections. When counter is set, it adds
// the bytes read from src.
func (t *LazySSMTunnel) copyData(dst, src net.Conn, errCh chan<- error, counter *atomic.Int64) {
	var r io.Reader = src
	if counter != nil {
		r = countingReader{r: src, n: counter}
	}
	_, err := io.Copy(dst, r)
	errCh <- err

	// Update activity on each data transfer
	t.lastActivity.Store(time.Now().Unix())
}

// Start begins listening for connections
func (t *LazySSMTunnel) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp",
		net.JoinHostPort(t.appConfig.Network.ListenAddress, strconv.Itoa(t.config.LocalPort)))
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

// shouldCloseIdle reports whether an idle tunnel should be reaped now.
//
// A tunnel is reaped only when it has no open connections and has been idle
// longer than idleTimeout. When keepalive > 0, the tunnel is held open for at
// least keepalive seconds from startedAt (the SSM process start time), so the
// idle timeout is suppressed while inside that window. keepalive or startedAt
// of 0 disables the minimum-lifetime hold, preserving the original behaviour.
func shouldCloseIdle(now, lastActivity int64, connections int32, idleTimeout, keepalive, startedAt int64) bool {
	if connections != 0 || lastActivity == 0 {
		return false
	}
	if now-lastActivity <= idleTimeout {
		return false
	}
	if keepalive > 0 && startedAt > 0 && now-startedAt < keepalive {
		return false // still inside keepalive window
	}
	return true
}

// monitorIdle closes the tunnel after inactivity
func (t *LazySSMTunnel) monitorIdle(ctx context.Context) {
	ticker := time.NewTicker(t.appConfig.Timeouts.IdleCheck)
	defer ticker.Stop()

	idleTimeout := int64(t.appConfig.Timeouts.IdleTimeout.Seconds())
	keepalive := int64(t.config.Keepalive)

	for {
		select {
		case <-ctx.Done():
			t.shutdown()
			return
		case <-ticker.C:
			if shouldCloseIdle(time.Now().Unix(), t.lastActivity.Load(), t.connections.Load(),
				idleTimeout, keepalive, t.tunnelStartedAt.Load()) {
				log.Printf("[%s] Idle timeout reached, closing tunnel", t.config.Description)
				t.closeTunnel()
				t.lastActivity.Store(0)
			}
		}
	}
}

// closeTunnel terminates the SSM tunnel on purpose (idle, reload, shutdown).
// The exit watcher sees the mark and logs it as a normal stop.
func (t *LazySSMTunnel) closeTunnel() {
	t.mu.Lock()
	defer t.mu.Unlock()

	cmd, exit := t.tunnelCmd, t.tunnelExit
	if cmd == nil || cmd.Process == nil {
		return
	}
	log.Printf("[%s] Terminating SSM tunnel", t.config.Description)
	t.stoppedCmd = cmd
	killProcessGroup(cmd)
	// The watcher closes exit before it takes t.mu, so waiting here while
	// holding the lock cannot deadlock.
	<-exit
	t.tunnelCmd = nil
	t.tunnelStartedAt.Store(0)
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
