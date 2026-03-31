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
