package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/diagnose"
	"github.com/antero-software/lazy-ssm/sso"
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

// fakeDiagnosis returns report, optionally waiting for release first.
type fakeDiagnosis struct {
	mu      sync.Mutex
	calls   int
	report  diagnose.Report
	release chan struct{}
	started chan struct{}
}

func (f *fakeDiagnosis) run(context.Context) diagnose.Report {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	return f.report
}

func (f *fakeDiagnosis) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func diagTunnel(rec *problemRecorder, diag *fakeDiagnosis) *LazySSMTunnel {
	tun := testTunnel(rec)
	tun.runDiagnosis = diag.run
	return tun
}

func failReport(msg, fix string) diagnose.Report {
	return diagnose.Report{Results: []diagnose.Result{{Check: "Same VPC", Status: diagnose.Fail, Message: msg, Fix: fix}}}
}

func TestReportFailure_ShowsHeadline(t *testing.T) {
	rec := &problemRecorder{}
	tun := diagTunnel(rec, &fakeDiagnosis{report: failReport("m", "f")})

	tun.reportFailure(errors.New("boom"), triggerStart)
	tun.diagWG.Wait()

	if rec.count() != 1 || rec.calls[0] != [3]string{"staging-db", "Staging DB: cannot connect", "m\n\nf"} {
		t.Errorf("problem calls = %q", rec.calls)
	}
}

func TestReportFailure_NoFixHasNoBlankLines(t *testing.T) {
	rec := &problemRecorder{}
	tun := diagTunnel(rec, &fakeDiagnosis{report: failReport("m", "")})

	tun.reportFailure(errors.New("boom"), triggerStart)
	tun.diagWG.Wait()

	if rec.count() != 1 || rec.calls[0][2] != "m" {
		t.Errorf("problem calls = %q", rec.calls)
	}
}

func TestReportFailure_LoginDeclinedOrPausedLogsOnly(t *testing.T) {
	for _, cause := range []error{sso.ErrLoginDeclined, sso.ErrLoginPaused} {
		rec := &problemRecorder{}
		diag := &fakeDiagnosis{report: failReport("m", "f")}
		tun := diagTunnel(rec, diag)

		tun.reportFailure(fmt.Errorf("SSO authentication failed: SSO login failed: %w", cause), triggerStart)
		tun.diagWG.Wait()

		if diag.count() != 0 || rec.count() != 0 {
			t.Errorf("%v: diagnoses = %d, problems = %d, want 0 and 0", cause, diag.count(), rec.count())
		}
	}
}

func TestReportFailure_OneDiagnosisAtATime(t *testing.T) {
	rec := &problemRecorder{}
	diag := &fakeDiagnosis{report: failReport("m", "f"), release: make(chan struct{}), started: make(chan struct{}, 2)}
	tun := diagTunnel(rec, diag)

	tun.reportFailure(errors.New("first"), triggerStart)
	<-diag.started
	tun.reportFailure(errors.New("second"), triggerPlugin)
	close(diag.release)
	tun.diagWG.Wait()

	if diag.count() != 1 {
		t.Errorf("diagnoses = %d, want 1", diag.count())
	}
	if rec.count() != 1 {
		t.Errorf("problems = %d, want 1", rec.count())
	}
}

func TestReportFailure_CooldownAfterDiagnosis(t *testing.T) {
	rec := &problemRecorder{}
	diag := &fakeDiagnosis{report: failReport("m", "f")}
	tun := diagTunnel(rec, diag)

	tun.reportFailure(errors.New("first"), triggerStart)
	tun.diagWG.Wait()
	tun.reportFailure(errors.New("again"), triggerStart)
	tun.diagWG.Wait()

	if diag.count() != 1 {
		t.Errorf("diagnoses within the cooldown = %d, want 1", diag.count())
	}
}

func TestReportFailure_HeuristicAllPassLogsOnly(t *testing.T) {
	rec := &problemRecorder{}
	tun := diagTunnel(rec, &fakeDiagnosis{})

	tun.reportFailure(errNoResponse, triggerHeuristic)
	tun.diagWG.Wait()

	if rec.count() != 0 {
		t.Errorf("problems = %q, want none", rec.calls)
	}
}

func TestReportFailure_StrongTriggerDuringHeuristicRunShowsAllPassed(t *testing.T) {
	rec := &problemRecorder{}
	diag := &fakeDiagnosis{release: make(chan struct{}), started: make(chan struct{}, 1)}
	tun := diagTunnel(rec, diag)

	tun.reportFailure(errNoResponse, triggerHeuristic)
	<-diag.started
	tun.reportFailure(errDestination, triggerPlugin)
	close(diag.release)
	tun.diagWG.Wait()

	if rec.count() != 1 || rec.calls[0][2] != diagnose.AllPassedMessage+"\n\n"+errDestination.Error() {
		t.Errorf("problems = %q", rec.calls)
	}
}

func TestReportFailure_StartAllPassShowsAllPassedAndCause(t *testing.T) {
	rec := &problemRecorder{}
	tun := diagTunnel(rec, &fakeDiagnosis{})
	cause := errors.New("tunnel failed to become ready after 15s")

	tun.reportFailure(cause, triggerStart)
	tun.diagWG.Wait()

	if rec.count() != 1 || rec.calls[0][2] != diagnose.AllPassedMessage+"\n\n"+cause.Error() {
		t.Errorf("problems = %q", rec.calls)
	}
}

func TestIsDestinationFailure(t *testing.T) {
	tests := map[string]bool{
		"Connection to destination port failed, check SSM Agent logs.": true,
		"connection to destination port FAILED":                        true,
		"Port 15432 opened for sessionId keith-0abc.":                  false,
	}
	for line, want := range tests {
		if got := isDestinationFailure(line); got != want {
			t.Errorf("isDestinationFailure(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestScanTunnelOutput_DestinationLineTriggersDiagnosis(t *testing.T) {
	rec := &problemRecorder{}
	diag := &fakeDiagnosis{report: failReport("m", "f")}
	tun := diagTunnel(rec, diag)

	tun.scanTunnelOutput(strings.NewReader("Starting session with SessionId: x\nConnection to destination port failed, check SSM Agent logs.\n"))
	tun.diagWG.Wait()

	if diag.count() != 1 || rec.count() != 1 {
		t.Errorf("diagnoses = %d, problems = %d, want 1 and 1", diag.count(), rec.count())
	}
}

func TestNoResponse(t *testing.T) {
	tests := []struct {
		name               string
		fromDB, fromClient int64
		lasted             time.Duration
		want               bool
	}{
		{"client sent, database silent, client gave up", 0, 5, 25 * time.Second, true},
		{"server-first protocol never greeted", 0, 0, 25 * time.Second, true},
		{"client sent, connection dropped at once", 0, 5, 100 * time.Millisecond, true},
		{"instant port probe", 0, 0, 200 * time.Millisecond, false},
		{"database answered", 12, 5, time.Second, false},
	}
	for _, tt := range tests {
		if got := noResponse(tt.fromDB, tt.fromClient, tt.lasted); got != tt.want {
			t.Errorf("%s: noResponse = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestManagerStart_ListenErrorShowsProblem(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		Network: config.NetworkConfig{ListenAddress: "127.0.0.1", TunnelPortOffset: 10000},
		Tunnels: []config.TunnelConfig{{Name: "staging-db", Description: "Staging DB", LocalPort: port}},
	}
	tm := NewTunnelManager(cfg)
	rec := &problemRecorder{}
	tm.tunnels[0].problem = rec.record

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tm.Start(ctx)

	if rec.count() != 1 {
		t.Fatalf("problems = %q, want 1", rec.calls)
	}
	want := fmt.Sprintf("lazy-ssm cannot listen on local port %d. Another program may be using it.", port)
	if rec.calls[0][1] != "Staging DB: cannot connect" || !strings.HasPrefix(rec.calls[0][2], want) {
		t.Errorf("problem = %q", rec.calls[0])
	}
}

func TestCloseTunnel_StopsChildProcesses(t *testing.T) {
	rec := &problemRecorder{}
	tun := testTunnel(rec)
	pidFile := t.TempDir() + "/child.pid"

	// Like aws starting session-manager-plugin: the child outlives a kill of the parent alone.
	tun.mu.Lock()
	err := tun.startProcess(exec.Command("sh", "-c", "sleep 60 & echo $! > "+pidFile+"; wait"))
	tun.mu.Unlock()
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}

	var childPID int
	deadline := time.Now().Add(2 * time.Second)
	for childPID == 0 {
		if b, err := os.ReadFile(pidFile); err == nil {
			fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &childPID)
		}
		if time.Now().After(deadline) {
			t.Fatal("child never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	tun.closeTunnel()

	deadline = time.Now().Add(2 * time.Second)
	for syscall.Kill(childPID, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(childPID, syscall.SIGKILL)
			t.Fatalf("child process %d still running after closeTunnel", childPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStart_CancelStopsSSMProcessBeforeReturning(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()

	cfg := &config.Config{
		Network:  config.NetworkConfig{ListenAddress: "127.0.0.1", TunnelPortOffset: 10000},
		Timeouts: config.TimeoutConfig{IdleCheck: time.Hour, Shutdown: 2 * time.Second, ShutdownPoll: 10 * time.Millisecond},
	}
	tun := NewLazySSMTunnel(config.TunnelConfig{Name: "staging-db", LocalPort: port}, cfg)
	tun.problem = (&problemRecorder{}).record

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tun.Start(ctx); close(done) }()

	tun.mu.Lock()
	err = tun.startProcess(exec.Command("sleep", "60"))
	cmd := tun.tunnelCmd
	tun.mu.Unlock()
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after cancel")
	}

	if syscall.Kill(cmd.Process.Pid, 0) == nil {
		cmd.Process.Kill()
		t.Fatal("SSM process still running after Start returned")
	}
}
