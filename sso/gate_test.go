package sso

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antero-software/lazy-ssm/notify"
)

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

type checkerHarness struct {
	checker   *AuthChecker
	clock     *fakeClock
	askCalls  *atomic.Int32
	loginCall *atomic.Int32
}

// newChecker builds an AuthChecker whose ask returns choice (after block is
// closed, when block is set) and whose login returns loginErr.
func newChecker(choice notify.LoginChoice, loginErr error, block chan struct{}) checkerHarness {
	clock := &fakeClock{t: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)}
	var asks, logins atomic.Int32
	a := &AuthChecker{
		cliPath: "aws",
		ask: func(context.Context, string) notify.LoginChoice {
			asks.Add(1)
			if block != nil {
				<-block
			}
			return choice
		},
		login: func(context.Context, string) error {
			logins.Add(1)
			return loginErr
		},
		gate: newLoginGate(clock.now),
	}
	return checkerHarness{checker: a, clock: clock, askCalls: &asks, loginCall: &logins}
}

func TestPromptAndLogin_LoginNowRunsLoginOnce(t *testing.T) {
	h := newChecker(notify.LoginNow, nil, nil)

	if err := h.checker.promptAndLogin(context.Background(), "staging"); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := h.loginCall.Load(); got != 1 {
		t.Errorf("login calls = %d, want 1", got)
	}
}

func TestPromptAndLogin_LaterReturnsDeclined(t *testing.T) {
	h := newChecker(notify.LoginLater, nil, nil)

	err := h.checker.promptAndLogin(context.Background(), "staging")
	if !errors.Is(err, ErrLoginDeclined) {
		t.Fatalf("err = %v, want ErrLoginDeclined", err)
	}
	if got := h.loginCall.Load(); got != 0 {
		t.Errorf("login calls = %d, want 0", got)
	}
}

func TestPromptAndLogin_PausedFor5Minutes(t *testing.T) {
	h := newChecker(notify.LoginLater, nil, nil)
	start := h.clock.now()

	_ = h.checker.promptAndLogin(context.Background(), "staging")

	h.clock.set(start.Add(4*time.Minute + 59*time.Second))
	if err := h.checker.promptAndLogin(context.Background(), "staging"); !errors.Is(err, ErrLoginPaused) {
		t.Fatalf("at 4m59s err = %v, want ErrLoginPaused", err)
	}
	if got := h.askCalls.Load(); got != 1 {
		t.Fatalf("at 4m59s ask calls = %d, want 1", got)
	}

	h.clock.set(start.Add(5*time.Minute + time.Second))
	_ = h.checker.promptAndLogin(context.Background(), "staging")
	if got := h.askCalls.Load(); got != 2 {
		t.Fatalf("at 5m01s ask calls = %d, want 2", got)
	}
}

func TestPromptAndLogin_SameProfileSharesOneDialog(t *testing.T) {
	block := make(chan struct{})
	h := newChecker(notify.LoginNow, nil, block)

	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() { errs <- h.checker.promptAndLogin(context.Background(), "staging") }()
	}

	// Wait until one caller is in the dialog and the other two are waiting on it.
	deadline := time.Now().Add(2 * time.Second)
	for h.askCalls.Load() != 1 || h.checker.gate.waiters("staging") != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("callers did not line up: asks=%d waiters=%d", h.askCalls.Load(), h.checker.gate.waiters("staging"))
		}
		time.Sleep(time.Millisecond)
	}
	close(block)

	for i := 0; i < 3; i++ {
		if err := <-errs; err != nil {
			t.Errorf("caller %d err = %v, want nil", i, err)
		}
	}
	if got := h.askCalls.Load(); got != 1 {
		t.Errorf("ask calls = %d, want 1", got)
	}
	if got := h.loginCall.Load(); got != 1 {
		t.Errorf("login calls = %d, want 1", got)
	}
}

func TestPromptAndLogin_DifferentProfilesIndependent(t *testing.T) {
	h := newChecker(notify.LoginLater, nil, nil)

	_ = h.checker.promptAndLogin(context.Background(), "a")
	err := h.checker.promptAndLogin(context.Background(), "b")

	if !errors.Is(err, ErrLoginDeclined) {
		t.Fatalf("profile b err = %v, want ErrLoginDeclined (asked, not paused)", err)
	}
	if got := h.askCalls.Load(); got != 2 {
		t.Errorf("ask calls = %d, want 2", got)
	}
}

func TestPromptAndLogin_LoginErrorDoesNotPause(t *testing.T) {
	loginErr := errors.New("SSO login command failed")
	h := newChecker(notify.LoginNow, loginErr, nil)

	err := h.checker.promptAndLogin(context.Background(), "staging")
	if !errors.Is(err, loginErr) || errors.Is(err, ErrLoginDeclined) {
		t.Fatalf("err = %v, want the login error", err)
	}

	_ = h.checker.promptAndLogin(context.Background(), "staging")
	if got := h.askCalls.Load(); got != 2 {
		t.Errorf("ask calls = %d, want 2 (no pause after a failed login)", got)
	}
}
