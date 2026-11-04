package notify

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// fakeRunner records every dialog it is asked to show.
type fakeRunner struct {
	mu     sync.Mutex
	specs  []DialogSpec
	result DialogResult
	err    error
	block  chan struct{} // when set, Show waits until it is closed
}

func (f *fakeRunner) Show(_ context.Context, d DialogSpec) (DialogResult, error) {
	f.mu.Lock()
	f.specs = append(f.specs, d)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return f.result, f.err
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.specs)
}

// fakeClock is a settable clock safe for use across goroutines.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

type uiHarness struct {
	ui        *UI
	runner    *fakeRunner
	clock     *fakeClock
	fallback  *[]string
	openCalls *int
}

func newHarness(runner *fakeRunner) uiHarness {
	var calls []string
	var mu sync.Mutex
	fallback := testNotifier(30*time.Second, &calls)
	fallback.dispatch = func(title, body, _ string) {
		mu.Lock()
		calls = append(calls, title+"\x00"+body)
		mu.Unlock()
	}
	clock := &fakeClock{t: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)}
	openCalls := 0
	var dr DialogRunner
	if runner != nil {
		dr = runner
	}
	ui := NewUI(dr, fallback, func() error { openCalls++; return nil }, clock.now)
	return uiHarness{ui: ui, runner: runner, clock: clock, fallback: &calls, openCalls: &openCalls}
}

func TestAskLogin_DialogCopy(t *testing.T) {
	r := &fakeRunner{result: DialogResult{Button: "Log in"}}
	h := newHarness(r)

	h.ui.AskLogin(context.Background(), "staging")

	want := DialogSpec{
		Title:       "lazy-ssm",
		Message:     "AWS login needed for profile 'staging'.",
		Buttons:     []string{"Later", "Log in"},
		Default:     "Log in",
		GiveUpAfter: 2 * time.Minute,
	}
	if r.count() != 1 || !reflect.DeepEqual(r.specs[0], want) {
		t.Fatalf("dialog specs = %+v, want one %+v", r.specs, want)
	}
}

func TestAskLogin_Choices(t *testing.T) {
	tests := []struct {
		name   string
		result DialogResult
		want   LoginChoice
	}{
		{"log in", DialogResult{Button: "Log in"}, LoginNow},
		{"later", DialogResult{Button: "Later"}, LoginLater},
		{"gave up", DialogResult{GaveUp: true}, LoginLater},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(&fakeRunner{result: tt.result})
			if got := h.ui.AskLogin(context.Background(), "p"); got != tt.want {
				t.Errorf("AskLogin = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAskLogin_RunnerErrorFallsBackToLoginNow(t *testing.T) {
	h := newHarness(&fakeRunner{err: errors.New("no GUI session")})

	if got := h.ui.AskLogin(context.Background(), "p"); got != LoginNow {
		t.Errorf("AskLogin = %v, want LoginNow", got)
	}
	if len(*h.fallback) != 1 {
		t.Errorf("fallback notifications = %d, want 1", len(*h.fallback))
	}
}

func TestAskLogin_NilRunnerFallsBackToLoginNow(t *testing.T) {
	h := newHarness(nil)

	if got := h.ui.AskLogin(context.Background(), "p"); got != LoginNow {
		t.Errorf("AskLogin = %v, want LoginNow", got)
	}
	if len(*h.fallback) != 1 {
		t.Errorf("fallback notifications = %d, want 1", len(*h.fallback))
	}
}

func TestProblem_DialogCopy(t *testing.T) {
	r := &fakeRunner{result: DialogResult{Button: "OK"}}
	h := newHarness(r)

	h.ui.Problem("staging-db", "Staging DB: cannot connect", "m")
	h.ui.wait()

	want := DialogSpec{
		Title:       "Staging DB: cannot connect",
		Message:     "m",
		Buttons:     []string{"Show logs", "OK"},
		Default:     "OK",
		GiveUpAfter: 10 * time.Minute,
	}
	if r.count() != 1 || !reflect.DeepEqual(r.specs[0], want) {
		t.Fatalf("dialog specs = %+v, want one %+v", r.specs, want)
	}
}

func TestProblem_OneOpenPerTunnel(t *testing.T) {
	r := &fakeRunner{result: DialogResult{Button: "OK"}, block: make(chan struct{})}
	h := newHarness(r)

	h.ui.Problem("a", "A: cannot connect", "m1")
	waitForShows(t, r, 1)
	h.ui.Problem("a", "A: cannot connect", "m2")
	if got := r.count(); got != 1 {
		t.Errorf("shows after second problem for same tunnel = %d, want 1", got)
	}
	h.ui.Problem("b", "B: cannot connect", "m1")
	waitForShows(t, r, 2)

	close(r.block)
	h.ui.wait()
	if got := r.count(); got != 2 {
		t.Errorf("total shows = %d, want 2", got)
	}
}

func TestProblem_SameFindingCooldown2Minutes(t *testing.T) {
	r := &fakeRunner{result: DialogResult{Button: "OK"}}
	h := newHarness(r)
	closedAt := h.clock.now()

	h.ui.Problem("a", "A: cannot connect", "m")
	h.ui.wait() // closes at closedAt

	h.clock.set(closedAt.Add(time.Second))
	h.ui.Problem("a", "A: cannot connect", "different")
	h.ui.wait()
	if got := r.count(); got != 2 {
		t.Fatalf("different finding within cooldown: shows = %d, want 2", got)
	}

	h.clock.set(closedAt.Add(time.Minute + 59*time.Second))
	h.ui.Problem("a", "A: cannot connect", "m")
	h.ui.wait()
	if got := r.count(); got != 2 {
		t.Fatalf("same finding at 1m59s: shows = %d, want 2", got)
	}

	h.clock.set(closedAt.Add(2*time.Minute + time.Second))
	h.ui.Problem("a", "A: cannot connect", "m")
	h.ui.wait()
	if got := r.count(); got != 3 {
		t.Fatalf("same finding at 2m01s: shows = %d, want 3", got)
	}
}

func TestProblem_ShowLogsOpensLogs(t *testing.T) {
	h := newHarness(&fakeRunner{result: DialogResult{Button: "Show logs"}})

	h.ui.Problem("a", "A: cannot connect", "m")
	h.ui.wait()

	if *h.openCalls != 1 {
		t.Errorf("openLogs calls = %d, want 1", *h.openCalls)
	}
}

func TestProblem_OKDoesNotOpenLogs(t *testing.T) {
	h := newHarness(&fakeRunner{result: DialogResult{Button: "OK"}})

	h.ui.Problem("a", "A: cannot connect", "m")
	h.ui.wait()

	if *h.openCalls != 0 {
		t.Errorf("openLogs calls = %d, want 0", *h.openCalls)
	}
}

func TestProblem_RunnerErrorFallsBackToNotification(t *testing.T) {
	h := newHarness(&fakeRunner{err: errors.New("no GUI session")})

	h.ui.Problem("a", "A: cannot connect", "m")
	h.ui.wait()

	if len(*h.fallback) != 1 || (*h.fallback)[0] != "A: cannot connect\x00m" {
		t.Errorf("fallback = %q, want one notification", *h.fallback)
	}
}

func TestProblem_NilRunnerUsesNotification(t *testing.T) {
	h := newHarness(nil)

	h.ui.Problem("a", "A: cannot connect", "m")
	h.ui.wait()

	if len(*h.fallback) != 1 || (*h.fallback)[0] != "A: cannot connect\x00m" {
		t.Errorf("fallback = %q, want one notification", *h.fallback)
	}
}

func waitForShows(t *testing.T, r *fakeRunner, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for r.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d shows, have %d", n, r.count())
		}
		time.Sleep(time.Millisecond)
	}
}
