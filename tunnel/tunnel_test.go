package tunnel

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antero-software/lazy-ssm/config"
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

// problemRecorder counts calls to a tunnel's problem hook.
type problemRecorder struct {
	mu    sync.Mutex
	calls [][3]string
}

func (p *problemRecorder) record(tunnel, title, message string) {
	p.mu.Lock()
	p.calls = append(p.calls, [3]string{tunnel, title, message})
	p.mu.Unlock()
}

func (p *problemRecorder) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func testTunnel(rec *problemRecorder) *LazySSMTunnel {
	tun := NewLazySSMTunnel(config.TunnelConfig{Name: "staging-db", Description: "Staging DB"}, &config.Config{})
	tun.problem = rec.record
	return tun
}

func TestCloseTunnel_NoProblemOnIntentionalStop(t *testing.T) {
	rec := &problemRecorder{}
	tun := testTunnel(rec)

	tun.mu.Lock()
	err := tun.startProcess(exec.Command("sleep", "60"))
	tun.mu.Unlock()
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}

	done := make(chan struct{})
	go func() { tun.closeTunnel(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("closeTunnel did not return within 2s")
	}

	time.Sleep(50 * time.Millisecond) // let the exit watcher finish
	if rec.count() != 0 {
		t.Errorf("problem calls = %d, want 0", rec.count())
	}
}

func TestUnexpectedExit_LogsOnlyAndClearsState(t *testing.T) {
	rec := &problemRecorder{}
	tun := testTunnel(rec)

	tun.mu.Lock()
	err := tun.startProcess(exec.Command("sh", "-c", "exit 3"))
	tun.mu.Unlock()
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		tun.mu.Lock()
		cleared := tun.tunnelCmd == nil
		tun.mu.Unlock()
		if cleared && tun.tunnelStartedAt.Load() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("state not cleared after the process exited")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rec.count() != 0 {
		t.Errorf("problem calls = %d, want 0", rec.count())
	}
}

func TestScanTunnelOutput_ErrorLinesDoNotPopUp(t *testing.T) {
	rec := &problemRecorder{}
	tun := testTunnel(rec)

	tun.scanTunnelOutput(strings.NewReader("An error occurred (TargetNotConnected) when calling the StartSession operation\n"))

	if rec.count() != 0 {
		t.Errorf("problem calls = %d, want 0", rec.count())
	}
}

func TestDisplayName(t *testing.T) {
	withDesc := NewLazySSMTunnel(config.TunnelConfig{Name: "staging-db", Description: "Staging DB"}, &config.Config{})
	if got := withDesc.displayName(); got != "Staging DB" {
		t.Errorf("displayName = %q, want Staging DB", got)
	}
	noDesc := NewLazySSMTunnel(config.TunnelConfig{Name: "staging-db"}, &config.Config{})
	if got := noDesc.displayName(); got != "staging-db" {
		t.Errorf("displayName = %q, want staging-db", got)
	}
}
