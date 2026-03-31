package tunnel

import (
	"context"
	"log"
	"sync"

	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/notify"
)

// TunnelManager manages multiple tunnels
type TunnelManager struct {
	tunnels   []*LazySSMTunnel
	wg        sync.WaitGroup
	mu        sync.RWMutex
	cfg       *config.Config
	ctx       context.Context
	cancel    context.CancelFunc
	reloadCh  chan struct{}
}

// NewTunnelManager creates a new tunnel manager
func NewTunnelManager(cfg *config.Config) *TunnelManager {
	tm := &TunnelManager{
		cfg:      cfg,
		tunnels:  make([]*LazySSMTunnel, len(cfg.Tunnels)),
		reloadCh: make(chan struct{}, 1),
	}

	for i, tunnelConfig := range cfg.Tunnels {
		tm.tunnels[i] = NewLazySSMTunnel(tunnelConfig, cfg)
	}

	return tm
}

// Start starts all tunnels
func (tm *TunnelManager) Start(ctx context.Context) error {
	tm.mu.Lock()
	tm.ctx, tm.cancel = context.WithCancel(ctx)
	tm.mu.Unlock()

	for _, tunnel := range tm.tunnels {
		tm.wg.Add(1)
		go func(t *LazySSMTunnel) {
			defer tm.wg.Done()
			if err := t.Start(tm.ctx); err != nil && err != context.Canceled {
				log.Printf("Tunnel error: %v", err)
				notify.Error("lazy-ssm error", err.Error())
			}
		}(tunnel)
	}

	tm.wg.Wait()
	return nil
}

// Reload reloads the configuration and restarts tunnels
func (tm *TunnelManager) Reload(configPath, profile, region string) error {
	log.Println("Reloading configuration...")

	// Load new configuration
	newCfg, err := config.Load(configPath, profile, region)
	if err != nil {
		return err
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Cancel current context to stop all tunnels
	if tm.cancel != nil {
		log.Println("Stopping existing tunnels...")
		tm.cancel()
		tm.wg.Wait()
	}

	// Update configuration
	tm.cfg = newCfg

	// Create new tunnels
	tm.tunnels = make([]*LazySSMTunnel, len(newCfg.Tunnels))
	for i, tunnelConfig := range newCfg.Tunnels {
		tm.tunnels[i] = NewLazySSMTunnel(tunnelConfig, newCfg)
	}

	log.Printf("Reloaded %d tunnel(s) from configuration", len(tm.tunnels))

	// Start new tunnels with a new context
	tm.ctx, tm.cancel = context.WithCancel(context.Background())

	for _, tunnel := range tm.tunnels {
		tm.wg.Add(1)
		go func(t *LazySSMTunnel) {
			defer tm.wg.Done()
			if err := t.Start(tm.ctx); err != nil && err != context.Canceled {
				log.Printf("Tunnel error: %v", err)
				notify.Error("lazy-ssm error", err.Error())
			}
		}(tunnel)
	}

	log.Println("Configuration reloaded successfully")
	return nil
}

// Stop gracefully stops all tunnels
func (tm *TunnelManager) Stop() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.cancel != nil {
		log.Println("Stopping all tunnels...")
		tm.cancel()
		tm.wg.Wait()
		log.Println("All tunnels stopped")
	}
}

// GetStatus returns the current status of all tunnels
func (tm *TunnelManager) GetStatus() map[string]interface{} {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tunnelStatus := make([]map[string]interface{}, len(tm.tunnels))
	for i, t := range tm.tunnels {
		tunnelStatus[i] = map[string]interface{}{
			"name":         t.config.Name,
			"description":  t.config.Description,
			"local_port":   t.config.LocalPort,
			"rds_endpoint": t.config.RDSEndpoint,
			"connections":  t.connections.Load(),
		}
	}

	return map[string]interface{}{
		"tunnel_count": len(tm.tunnels),
		"tunnels":      tunnelStatus,
	}
}
