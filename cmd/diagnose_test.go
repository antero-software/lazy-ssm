package cmd

import (
	"testing"

	"github.com/antero-software/lazy-ssm/config"
)

func TestSelectTunnels(t *testing.T) {
	cfg := &config.Config{Tunnels: []config.TunnelConfig{{Name: "a"}, {Name: "b"}}}

	all, err := selectTunnels(cfg, "")
	if err != nil || len(all) != 2 {
		t.Errorf(`selectTunnels("") = (%v, %v), want both tunnels`, all, err)
	}

	one, err := selectTunnels(cfg, "b")
	if err != nil || len(one) != 1 || one[0].Name != "b" {
		t.Errorf(`selectTunnels("b") = (%v, %v), want [b]`, one, err)
	}

	_, err = selectTunnels(cfg, "x")
	if err == nil || err.Error() != "no tunnel named 'x'. Tunnels: a, b" {
		t.Errorf(`selectTunnels("x") err = %v`, err)
	}
}
