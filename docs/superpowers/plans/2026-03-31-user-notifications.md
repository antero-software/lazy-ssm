# User Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface tunnel errors and SSO login requests to the user via OS desktop notifications with 30-second per-message deduplication.

**Architecture:** The `notify` package gains a `Notifier` struct owning a deduplication map (protected by a mutex) and an injectable `dispatch` function for platform-specific sending. A package-level `defaultNotifier` keeps all call sites simple. `tunnel/tunnel.go` and `tunnel/manager.go` add `notify.Error()` calls alongside existing `log.Printf` error calls.

**Tech Stack:** Go standard library only — `sync`, `time`, `os/exec`, `runtime`. No new dependencies.

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `notify/notify.go` | Rewrite | `Notifier` struct, deduplication, platform dispatch, package-level wrappers |
| `notify/notify_test.go` | Create | Unit tests for deduplication and SSO URL passthrough |
| `tunnel/tunnel.go` | Modify | Add `notify.Error()` at 3 error sites + import |
| `tunnel/manager.go` | Modify | Add `notify.Error()` at 2 error sites (Start + Reload goroutines) + import |

---

### Task 1: Write failing tests for Notifier

**Files:**
- Create: `notify/notify_test.go`

- [ ] **Step 1: Create the test file**

```go
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
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `cd /Users/keith/Projects/lazy-ssm && go test ./notify/... -v`

Expected: compilation failure — `notify.Notifier` undefined (struct does not exist yet)

---

### Task 2: Implement Notifier in notify.go

**Files:**
- Modify: `notify/notify.go`

- [ ] **Step 1: Replace notify.go with the full implementation**

```go
// notify/notify.go
package notify

import (
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Notifier sends desktop notifications with per-message deduplication.
// The zero value is not usable; construct with New.
type Notifier struct {
	mu       sync.Mutex
	lastSent map[string]time.Time
	cooldown time.Duration
	dispatch func(title, body, ssoURL string)
}

// New creates a Notifier that suppresses repeated identical notifications
// within the given cooldown window.
func New(cooldown time.Duration) *Notifier {
	n := &Notifier{
		lastSent: make(map[string]time.Time),
		cooldown: cooldown,
	}
	n.dispatch = platformSend
	return n
}

// defaultNotifier backs the package-level Error and SSO functions.
var defaultNotifier = New(30 * time.Second)

// Error sends an error notification via the default Notifier.
// Repeated calls with the same title and message within 30 seconds are suppressed.
func Error(title, message string) { defaultNotifier.Error(title, message) }

// SSO sends an SSO login required notification via the default Notifier.
// Repeated calls for the same profile within 30 seconds are suppressed.
func SSO(profile, ssoURL string) { defaultNotifier.SSO(profile, ssoURL) }

// Error sends an error desktop notification, suppressing duplicates within the cooldown.
func (n *Notifier) Error(title, message string) {
	n.send(title, message, "")
}

// SSO sends an AWS SSO login required notification.
func (n *Notifier) SSO(profile, ssoURL string) {
	title := "lazy-ssm"
	body := fmt.Sprintf("AWS SSO login required for profile '%s'", profile)
	n.send(title, body, ssoURL)
}

// send deduplicates by title+body and dispatches if outside the cooldown window.
func (n *Notifier) send(title, body, ssoURL string) {
	key := title + "\x00" + body

	n.mu.Lock()
	if time.Since(n.lastSent[key]) < n.cooldown {
		n.mu.Unlock()
		return
	}
	n.lastSent[key] = time.Now()
	n.mu.Unlock()

	n.dispatch(title, body, ssoURL)
}

// platformSend dispatches a notification using the OS-native mechanism.
func platformSend(title, body, ssoURL string) {
	switch runtime.GOOS {
	case "darwin":
		sendDarwin(title, body, ssoURL)
	case "linux":
		sendLinux(title, body, ssoURL)
	}
}

func sendDarwin(title, body, ssoURL string) {
	if notifierPath, err := exec.LookPath("terminal-notifier"); err == nil {
		args := []string{"-title", title, "-message", body, "-sound", "default"}
		if ssoURL != "" {
			args = append(args, "-open", ssoURL)
		}
		if exec.Command(notifierPath, args...).Run() == nil {
			return
		}
	}
	script := fmt.Sprintf(`display notification %q with title %q sound name "default"`, body, title)
	exec.Command("osascript", "-e", script).Run() //nolint:errcheck
}

func sendLinux(title, body, ssoURL string) {
	msg := body
	if ssoURL != "" {
		msg = body + "\n" + ssoURL
	}
	exec.Command("notify-send", title, msg).Run() //nolint:errcheck
}
```

- [ ] **Step 2: Run tests to confirm they pass**

Run: `cd /Users/keith/Projects/lazy-ssm && go test ./notify/... -v`

Expected:
```
=== RUN   TestNotifier_DeduplicatesWithinCooldown
--- PASS: TestNotifier_DeduplicatesWithinCooldown
=== RUN   TestNotifier_SendsAfterCooldownExpires
--- PASS: TestNotifier_SendsAfterCooldownExpires
=== RUN   TestNotifier_DifferentMessagesNotDeduplicated
--- PASS: TestNotifier_DifferentMessagesNotDeduplicated
=== RUN   TestNotifier_SSO_PassesURLToDispatch
--- PASS: TestNotifier_SSO_PassesURLToDispatch
=== RUN   TestNotifier_SSO_Deduplicates
--- PASS: TestNotifier_SSO_Deduplicates
PASS
ok  	github.com/antero-software/lazy-ssm/notify
```

- [ ] **Step 3: Commit**

```bash
cd /Users/keith/Projects/lazy-ssm && \
git add notify/notify.go notify/notify_test.go && \
git commit -m "feat(notify): add Notifier struct with deduplication and Linux support"
```

---

### Task 3: Add notify.Error() to tunnel/tunnel.go

Three sites: process-exit goroutine, handleConnection, scanTunnelOutput.

**Files:**
- Modify: `tunnel/tunnel.go`

- [ ] **Step 1: Add notify to the import block**

Find:
```go
	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/ec2"
	"github.com/antero-software/lazy-ssm/sso"
```

Replace with:
```go
	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/ec2"
	"github.com/antero-software/lazy-ssm/notify"
	"github.com/antero-software/lazy-ssm/sso"
```

- [ ] **Step 2: Add notify.Error() in the process-exit goroutine**

Find:
```go
		if waitErr != nil {
			log.Printf("[%s] ERROR: SSM tunnel process exited: %v", t.config.Description, waitErr)
		} else {
			log.Printf("[%s] SSM tunnel process exited", t.config.Description)
		}
```

Replace with:
```go
		if waitErr != nil {
			log.Printf("[%s] ERROR: SSM tunnel process exited: %v", t.config.Description, waitErr)
			notify.Error(
				fmt.Sprintf("Tunnel error: %s", t.config.Description),
				fmt.Sprintf("SSM tunnel process exited: %v", waitErr),
			)
		} else {
			log.Printf("[%s] SSM tunnel process exited", t.config.Description)
		}
```

- [ ] **Step 3: Add notify.Error() in scanTunnelOutput for detected error lines**

Find:
```go
		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") ||
			strings.Contains(lower, "unable") || strings.Contains(lower, "cannot") {
			log.Printf("[%s] SSM ERROR: %s", t.config.Description, line)
		} else {
```

Replace with:
```go
		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") ||
			strings.Contains(lower, "unable") || strings.Contains(lower, "cannot") {
			log.Printf("[%s] SSM ERROR: %s", t.config.Description, line)
			notify.Error(
				fmt.Sprintf("Tunnel error: %s", t.config.Description),
				line,
			)
		} else {
```

- [ ] **Step 4: Add notify.Error() in handleConnection when ensureTunnel fails**

Find:
```go
	// Ensure tunnel is running
	if err := t.ensureTunnel(); err != nil {
		log.Printf("[%s] Failed to ensure tunnel: %v", t.config.Description, err)
		return
	}
```

Replace with:
```go
	// Ensure tunnel is running
	if err := t.ensureTunnel(); err != nil {
		log.Printf("[%s] Failed to ensure tunnel: %v", t.config.Description, err)
		notify.Error(
			fmt.Sprintf("Tunnel failed: %s", t.config.Description),
			err.Error(),
		)
		return
	}
```

- [ ] **Step 5: Build to verify no compilation errors**

Run: `cd /Users/keith/Projects/lazy-ssm && go build ./...`

Expected: no output (clean build)

- [ ] **Step 6: Commit**

```bash
cd /Users/keith/Projects/lazy-ssm && \
git add tunnel/tunnel.go && \
git commit -m "feat(tunnel): notify user on tunnel errors"
```

---

### Task 4: Add notify.Error() to tunnel/manager.go

Two sites: the tunnel goroutine in `Start()` and the identical goroutine in `Reload()`.

**Files:**
- Modify: `tunnel/manager.go`

- [ ] **Step 1: Add notify to the import block**

Find:
```go
	"github.com/antero-software/lazy-ssm/config"
```

Replace with:
```go
	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/notify"
```

- [ ] **Step 2: Add notify.Error() in the Start() goroutine**

Find (the goroutine inside `Start()` — has `tm.ctx` passed directly):
```go
		go func(t *LazySSMTunnel) {
			defer tm.wg.Done()
			if err := t.Start(tm.ctx); err != nil && err != context.Canceled {
				log.Printf("Tunnel error: %v", err)
			}
		}(tunnel)
	}

	tm.wg.Wait()
	return nil
```

Replace with:
```go
		go func(t *LazySSMTunnel) {
			defer tm.wg.Done()
			if err := t.Start(tm.ctx); err != nil && err != context.Canceled {
				log.Printf("Tunnel error: %v", err)
				notify.Error("lazy-ssm error", err.Error())
			}
		}(tunnel)
	}

	tm.wg.Wait()
	return nil
```

- [ ] **Step 3: Add notify.Error() in the Reload() goroutine**

Find (the goroutine inside `Reload()` — followed by `log.Println("Configuration reloaded successfully")`):
```go
		go func(t *LazySSMTunnel) {
			defer tm.wg.Done()
			if err := t.Start(tm.ctx); err != nil && err != context.Canceled {
				log.Printf("Tunnel error: %v", err)
			}
		}(tunnel)
	}

	log.Println("Configuration reloaded successfully")
```

Replace with:
```go
		go func(t *LazySSMTunnel) {
			defer tm.wg.Done()
			if err := t.Start(tm.ctx); err != nil && err != context.Canceled {
				log.Printf("Tunnel error: %v", err)
				notify.Error("lazy-ssm error", err.Error())
			}
		}(tunnel)
	}

	log.Println("Configuration reloaded successfully")
```

- [ ] **Step 4: Build and run all tests**

Run: `cd /Users/keith/Projects/lazy-ssm && go build ./... && go test ./...`

Expected:
```
ok  	github.com/antero-software/lazy-ssm/notify
```
All other packages: no test files (build-only verification sufficient).

- [ ] **Step 5: Commit**

```bash
cd /Users/keith/Projects/lazy-ssm && \
git add tunnel/manager.go && \
git commit -m "feat(tunnel): notify user on tunnel manager errors"
```
