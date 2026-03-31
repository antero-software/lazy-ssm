// notify/notify_test.go
package notify

import (
	"testing"
	"time"
)

func testNotifier(cooldown time.Duration, calls *[]string) *Notifier {
	return &Notifier{
		lastSent: make(map[string]time.Time),
		cooldown: cooldown,
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
	n := testNotifier(time.Millisecond, &calls)

	n.Error("Tunnel error: prod-db", "SSM tunnel process exited: signal: killed")
	time.Sleep(5 * time.Millisecond)
	n.Error("Tunnel error: prod-db", "SSM tunnel process exited: signal: killed")

	if len(calls) != 2 {
		t.Errorf("expected 2 notifications, got %d", len(calls))
	}
}

func TestNotifier_DifferentMessagesNotDeduplicated(t *testing.T) {
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
