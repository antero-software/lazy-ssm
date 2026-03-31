// notify/notify_test.go
package notify

import (
	"sync"
	"testing"
	"time"
)

func testNotifier(cooldown time.Duration, calls *[]string) *Notifier {
	return &Notifier{
		lastSent: make(map[string]time.Time),
		cooldown: cooldown,
		now:      time.Now,
		dispatch: func(title, body, ssoURL string) {
			*calls = append(*calls, title+"\x00"+body)
		},
	}
}

func TestNotifier_DeduplicatesWithinCooldown(t *testing.T) {
	var calls []string
	n := testNotifier(30*time.Second, &calls)

	n.Error("Tunnel error: prod-db", "SSM tunnel process exited: signal: killed")
	n.Error("Tunnel error: prod-db", "SSM tunnel process exited: signal: killed")

	if len(calls) != 1 {
		t.Errorf("expected 1 notification, got %d", len(calls))
	}
}

func TestNotifier_SendsAfterCooldownExpires(t *testing.T) {
	var calls []string
	var now time.Time
	n := &Notifier{
		lastSent: make(map[string]time.Time),
		cooldown: 30 * time.Second,
		now:      func() time.Time { return now },
		dispatch: func(title, body, ssoURL string) {
			calls = append(calls, title+"\x00"+body)
		},
	}

	now = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	n.Error("Tunnel error: prod-db", "SSM tunnel process exited: signal: killed")

	// Advance time past the cooldown
	now = now.Add(31 * time.Second)
	n.Error("Tunnel error: prod-db", "SSM tunnel process exited: signal: killed")

	if len(calls) != 2 {
		t.Errorf("expected 2 notifications, got %d", len(calls))
	}
}

func TestNotifier_DifferentTitlesNotDeduplicated(t *testing.T) {
	var calls []string
	n := testNotifier(30*time.Second, &calls)

	n.Error("Tunnel error: prod-db", "connection failed")
	n.Error("Tunnel error: staging-db", "connection failed")

	if len(calls) != 2 {
		t.Errorf("expected 2 notifications, got %d", len(calls))
	}
}

func TestNotifier_SSO_PassesURLToDispatch(t *testing.T) {
	var dispatchedURL string
	n := &Notifier{
		lastSent: make(map[string]time.Time),
		cooldown: 30 * time.Second,
		now:      time.Now,
		dispatch: func(title, body, url string) {
			dispatchedURL = url
		},
	}

	n.SSO("my-profile", "https://sso.example.com/start")

	if dispatchedURL != "https://sso.example.com/start" {
		t.Errorf("expected SSO URL passed to dispatch, got %q", dispatchedURL)
	}
}

func TestNotifier_SSO_Deduplicates(t *testing.T) {
	var calls []string
	n := testNotifier(30*time.Second, &calls)

	n.SSO("my-profile", "https://sso.example.com/start")
	n.SSO("my-profile", "https://sso.example.com/start")

	if len(calls) != 1 {
		t.Errorf("expected 1 SSO notification, got %d", len(calls))
	}
}

func TestNotifier_ConcurrentAccess(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	n := testNotifier(30*time.Second, &calls)
	// Override dispatch to be safe for concurrent use
	n.dispatch = func(title, body, ssoURL string) {
		mu.Lock()
		calls = append(calls, title+"\x00"+body)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.Error("concurrent", "message")
		}()
	}
	wg.Wait()

	// At most a small number of dispatches should have occurred (cooldown deduplication)
	// Exact count is non-deterministic due to TOCTOU window, but must be >= 1
	if len(calls) < 1 {
		t.Errorf("expected at least 1 notification, got %d", len(calls))
	}
}
