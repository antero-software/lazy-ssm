# Design: User Notifications for Errors and SSO Login

**Date:** 2026-03-31  
**Status:** Approved

## Problem

lazy-ssm runs as a headless Homebrew service. When errors occur (tunnel failures, SSM process exits, etc.) or AWS SSO login is required, nothing is surfaced to the user unless they actively inspect the logs.

## Goal

Surface errors and SSO login requests to the user via OS desktop notifications, with deduplication to prevent notification floods.

## Approach

Extend the existing `notify` package with a `Notifier` struct that manages deduplication state. A package-level `defaultNotifier` keeps call sites simple. Both macOS and Linux are supported.

---

## Design

### 1. `notify` Package — Notifier Struct

```go
type Notifier struct {
    mu       sync.Mutex
    lastSent map[string]time.Time // key = title + "\x00" + message
    cooldown time.Duration
}

func New(cooldown time.Duration) *Notifier

var defaultNotifier = New(30 * time.Second)

// Package-level convenience wrappers (keep existing call sites unchanged)
func Error(title, message string) { defaultNotifier.Error(title, message) }
func SSO(profile, ssoURL string)  { defaultNotifier.SSO(profile, ssoURL) }
```

The existing `notify.SSO()` signature is preserved — no changes required at its call site in `sso/auth.go`.

### 2. Deduplication

On each `Error()` or `SSO()` call:

1. Build deduplication key: `title + "\x00" + message`
2. Acquire lock; check `time.Since(lastSent[key]) < cooldown`
3. If within cooldown → silent drop, return
4. Otherwise → update `lastSent[key] = time.Now()`, release lock, send notification

Cooldown is **30 seconds per unique message**. Each distinct title+message pair has its own independent window, so different tunnels failing with different errors each get notified separately.

The map grows only as large as the number of distinct error messages in the app — no pruning needed.

### 3. Platform-Specific Notifications

**macOS** (`runtime.GOOS == "darwin"`):
- Try `terminal-notifier` first — supports `-open <url>` for click-to-browser on SSO notifications
- Fall back to `osascript` — works reliably from Homebrew service processes; no click action

**Linux** (`runtime.GOOS == "linux"`):
- Use `notify-send` — standard on GNOME/KDE desktops
- For SSO: include the URL in the notification body (notify-send has no click-to-URL support)
- Silent no-op if `notify-send` is not installed

**Other platforms:** silent no-op.

All notification failures are non-fatal and silently ignored.

### 4. Integration Points

`notify.Error()` is added alongside existing `log.Printf` calls at these locations:

| File | Line | Trigger | Notification Title |
|---|---|---|---|
| `tunnel/tunnel.go` | ~211 | SSM process exits with error | `"Tunnel error: <name>"` |
| `tunnel/tunnel.go` | ~231 | Tunnel process exits during startup | `"Tunnel failed: <name>"` |
| `tunnel/tunnel.go` | ~256 | Tunnel readiness timeout | `"Tunnel timeout: <name>"` |
| `tunnel/tunnel.go` | ~291 | `ensureTunnel()` fails on connection | `"Tunnel error: <name>"` |
| `tunnel/tunnel.go` | ~268-273 | `scanTunnelOutput` detects SSM ERROR line | `"Tunnel error: <name>"` |
| `tunnel/manager.go` | ~47 | Tunnel goroutine returns error | `"lazy-ssm error"` |

**Not changed:**
- `sso/auth.go:54` — `notify.SSO()` call stays as-is
- `cmd/root.go` — config load errors at startup are one-time failures best seen in `brew services log lazy-ssm`; no notification added

---

## Files Changed

| File | Change |
|---|---|
| `notify/notify.go` | Rewrite: add `Notifier` struct, `Error()` method, `New()`, update `SSO()` to use struct; keep package-level wrappers |
| `tunnel/tunnel.go` | Add `notify.Error()` calls at 5 error sites |
| `tunnel/manager.go` | Add `notify.Error()` call at tunnel goroutine error site |

---

## Out of Scope

- Linux systems running without a desktop environment (headless servers) — `notify-send` will silently fail, which is acceptable
- Click-to-browser on Linux SSO notifications
- Config load errors at startup
