package tunnel

import (
	"testing"

	"github.com/antero-software/lazy-ssm/ec2"
)

func TestShouldCloseIdle(t *testing.T) {
	const (
		idleTimeout = int64(300) // 5m
		now         = int64(10_000)
	)

	tests := []struct {
		name         string
		lastActivity int64
		connections  int32
		keepalive    int64
		startedAt    int64
		want         bool
	}{
		{
			name:         "open connection is never reaped",
			lastActivity: now - idleTimeout - 100,
			connections:  1,
			want:         false,
		},
		{
			name:         "no activity yet (lastActivity 0) is never reaped",
			lastActivity: 0,
			connections:  0,
			want:         false,
		},
		{
			name:         "idle within timeout stays open",
			lastActivity: now - (idleTimeout - 10),
			connections:  0,
			want:         false,
		},
		{
			name:         "idle beyond timeout, keepalive disabled -> reap (current behaviour)",
			lastActivity: now - (idleTimeout + 10),
			connections:  0,
			keepalive:    0,
			startedAt:    now - 1000,
			want:         true,
		},
		{
			name:         "idle beyond timeout but inside keepalive window -> held open",
			lastActivity: now - (idleTimeout + 10),
			connections:  0,
			keepalive:    600,
			startedAt:    now - 100, // 100s into a 600s window
			want:         false,
		},
		{
			name:         "idle beyond timeout and past keepalive window -> reap",
			lastActivity: now - (idleTimeout + 10),
			connections:  0,
			keepalive:    600,
			startedAt:    now - 700, // 700s > 600s window
			want:         true,
		},
		{
			name:         "keepalive set but startedAt 0 -> hold disabled, reap on idle",
			lastActivity: now - (idleTimeout + 10),
			connections:  0,
			keepalive:    600,
			startedAt:    0,
			want:         true,
		},
		{
			name:         "keepalive window active but connection open -> still not reaped",
			lastActivity: now - (idleTimeout + 10),
			connections:  2,
			keepalive:    600,
			startedAt:    now - 100,
			want:         false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldCloseIdle(now, tc.lastActivity, tc.connections, idleTimeout, tc.keepalive, tc.startedAt)
			if got != tc.want {
				t.Errorf("shouldCloseIdle() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDescribeEligible(t *testing.T) {
	got := describeEligible([]ec2.Instance{
		{ID: "i-1", Name: "bastion-prod-1", VPCID: "vpc-111", HasIAMProfile: true},
		{ID: "i-2", Name: "no-profile", VPCID: "vpc-111"},
		{ID: "i-3", Name: "bastion-stg-1", VPCID: "vpc-222", HasIAMProfile: true},
	})
	want := "bastion-prod-1 (i-1, vpc-111), bastion-stg-1 (i-3, vpc-222)"
	if got != want {
		t.Errorf("describeEligible = %q, want %q", got, want)
	}
}
