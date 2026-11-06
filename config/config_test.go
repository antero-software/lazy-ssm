package config

import "testing"

func TestProfileForRegionFor(t *testing.T) {
	cfg := &Config{AWS: AWSConfig{Profile: "global-p", Region: "global-r"}}

	withOverride := TunnelConfig{AWSProfile: "tunnel-p", AWSRegion: "tunnel-r"}
	if got := cfg.ProfileFor(withOverride); got != "tunnel-p" {
		t.Errorf("ProfileFor(override) = %q, want tunnel-p", got)
	}
	if got := cfg.RegionFor(withOverride); got != "tunnel-r" {
		t.Errorf("RegionFor(override) = %q, want tunnel-r", got)
	}

	if got := cfg.ProfileFor(TunnelConfig{}); got != "global-p" {
		t.Errorf("ProfileFor(empty) = %q, want global-p", got)
	}
	if got := cfg.RegionFor(TunnelConfig{}); got != "global-r" {
		t.Errorf("RegionFor(empty) = %q, want global-r", got)
	}
}
