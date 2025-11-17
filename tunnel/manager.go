package tunnel

import (
	"context"
	"log"
	"sync"

	"github.com/antero-software/lazy-ssm/config"
)

// TunnelManager manages multiple tunnels
type TunnelManager struct {
	tunnels []*LazySSMTunnel
	wg      sync.WaitGroup
}

// NewTunnelManager creates a new tunnel manager
func NewTunnelManager(cfg *config.Config) *TunnelManager {
	tm := &TunnelManager{
		tunnels: make([]*LazySSMTunnel, len(cfg.Tunnels)),
	}

	for i, tunnelConfig := range cfg.Tunnels {
		tm.tunnels[i] = NewLazySSMTunnel(tunnelConfig, cfg)
	}

	return tm
}

// Start starts all tunnels
func (tm *TunnelManager) Start(ctx context.Context) error {
	for _, tunnel := range tm.tunnels {
		tm.wg.Add(1)
		go func(t *LazySSMTunnel) {
			defer tm.wg.Done()
			if err := t.Start(ctx); err != nil && err != context.Canceled {
				log.Printf("Tunnel error: %v", err)
			}
		}(tunnel)
	}

	tm.wg.Wait()
	return nil
}
