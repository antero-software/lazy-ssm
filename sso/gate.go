package sso

import (
	"errors"
	"sync"
	"time"
)

// loginPause is how long "Later" stops new login prompts for a profile.
const loginPause = 5 * time.Minute

var (
	// ErrLoginDeclined means the user clicked Later, or did not answer the
	// login dialog in time.
	ErrLoginDeclined = errors.New("AWS login declined")
	// ErrLoginPaused means the user clicked Later less than 5 minutes ago,
	// so no new prompt was shown.
	ErrLoginPaused = errors.New("AWS login paused after Later")
)

// sharedGate is used by every AuthChecker, so tunnels that share a profile
// share one prompt and one login.
var sharedGate = newLoginGate(time.Now)

type loginCall struct {
	done    chan struct{}
	err     error
	waiters int
}

// loginGate makes sure only one login prompt runs per profile at a time and
// remembers when the user chose Later.
type loginGate struct {
	mu          sync.Mutex
	inflight    map[string]*loginCall
	pausedUntil map[string]time.Time
	now         func() time.Time
}

func newLoginGate(now func() time.Time) *loginGate {
	return &loginGate{
		inflight:    make(map[string]*loginCall),
		pausedUntil: make(map[string]time.Time),
		now:         now,
	}
}

// do runs fn once per profile at a time. Callers that arrive while fn runs
// wait and get the same error. If fn reports declined, do returns
// ErrLoginDeclined and pauses the profile for 5 minutes. While paused, do
// returns ErrLoginPaused without calling fn.
func (g *loginGate) do(profile string, fn func() (declined bool, err error)) error {
	g.mu.Lock()
	if g.now().Before(g.pausedUntil[profile]) {
		g.mu.Unlock()
		return ErrLoginPaused
	}
	if call, ok := g.inflight[profile]; ok {
		call.waiters++
		g.mu.Unlock()
		<-call.done
		return call.err
	}
	call := &loginCall{done: make(chan struct{})}
	g.inflight[profile] = call
	g.mu.Unlock()

	declined, err := fn()
	if declined {
		err = ErrLoginDeclined
	}

	g.mu.Lock()
	if declined {
		g.pausedUntil[profile] = g.now().Add(loginPause)
	}
	delete(g.inflight, profile)
	call.err = err
	g.mu.Unlock()
	close(call.done)
	return err
}

// waiters reports how many callers are waiting on the login in progress for
// profile. Used by tests.
func (g *loginGate) waiters(profile string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if call, ok := g.inflight[profile]; ok {
		return call.waiters
	}
	return 0
}
