package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/antero-software/lazy-ssm/logfile"
)

const (
	loginGiveUp     = 2 * time.Minute
	problemGiveUp   = 10 * time.Minute
	problemCooldown = 2 * time.Minute

	buttonLogin    = "Log in"
	buttonLater    = "Later"
	buttonShowLogs = "Show logs"
	buttonOK       = "OK"
)

// LoginChoice is what the user chose in the AWS login dialog.
type LoginChoice int

const (
	LoginNow LoginChoice = iota
	LoginLater
	// LoginAbort means lazy-ssm is stopping: do not log in, and do not
	// treat it as Later.
	LoginAbort
)

// UI shows the dialogs the user must act on and stops them from piling up.
// With no dialog runner (Linux, or no GUI session) it falls back to
// desktop notifications.
type UI struct {
	runner   DialogRunner
	fallback *Notifier
	openLogs func() error
	now      func() time.Time

	mu       sync.Mutex
	open     map[string]bool      // tunnel -> a Problem dialog is on screen
	closedAt map[string]time.Time // tunnel + "\x00" + message -> when its dialog closed
	wg       sync.WaitGroup
}

// NewUI creates a UI. runner is nil when dialogs are not available.
func NewUI(runner DialogRunner, fallback *Notifier, openLogs func() error, now func() time.Time) *UI {
	return &UI{
		runner:   runner,
		fallback: fallback,
		openLogs: openLogs,
		now:      now,
		open:     make(map[string]bool),
		closedAt: make(map[string]time.Time),
	}
}

// AskLogin asks the user whether to log in to AWS now. It blocks until the
// user clicks, or the dialog gives up (which counts as Later). If no dialog
// can be shown, it notifies and returns LoginNow, which keeps the old
// behaviour of starting the login straight away.
func (u *UI) AskLogin(ctx context.Context, profile string) LoginChoice {
	if ctx.Err() != nil {
		return LoginAbort
	}
	if u.runner == nil {
		u.fallback.SSO(profile, "")
		return LoginNow
	}

	res, err := u.runner.Show(ctx, DialogSpec{
		Title:       "lazy-ssm",
		Message:     fmt.Sprintf("AWS login needed for profile '%s'.", profile),
		Buttons:     []string{buttonLater, buttonLogin},
		Default:     buttonLogin,
		GiveUpAfter: loginGiveUp,
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, ErrDialogInterrupted) {
			log.Printf("[notify] login dialog closed while stopping: %v", err)
			return LoginAbort
		}
		log.Printf("[notify] dialog unavailable: %v", err)
		u.fallback.SSO(profile, "")
		return LoginNow
	}
	if res.Button == buttonLogin {
		return LoginNow
	}
	return LoginLater
}

// Problem shows a problem dialog for a tunnel and returns at once. Only one
// Problem dialog per tunnel is on screen at a time, and the same message for
// the same tunnel is not shown again within 2 minutes of its dialog closing.
func (u *UI) Problem(tunnel, title, message string) {
	if u.runner == nil {
		u.fallback.Error(title, message)
		return
	}

	key := tunnel + "\x00" + message
	u.mu.Lock()
	if u.open[tunnel] {
		u.mu.Unlock()
		log.Printf("[notify] problem dialog already open for %s, not showing: %s", tunnel, title)
		return
	}
	if closed, ok := u.closedAt[key]; ok && u.now().Sub(closed) < problemCooldown {
		u.mu.Unlock()
		return
	}
	u.open[tunnel] = true
	u.wg.Add(1)
	u.mu.Unlock()

	go func() {
		defer u.wg.Done()
		res, err := u.runner.Show(context.Background(), DialogSpec{
			Title:       title,
			Message:     message,
			Buttons:     []string{buttonShowLogs, buttonOK},
			Default:     buttonOK,
			GiveUpAfter: problemGiveUp,
		})

		u.mu.Lock()
		delete(u.open, tunnel)
		u.closedAt[key] = u.now()
		u.mu.Unlock()

		if err != nil {
			log.Printf("[notify] dialog unavailable: %v", err)
			u.fallback.Error(title, message)
			return
		}
		if res.Button == buttonShowLogs {
			if err := u.openLogs(); err != nil {
				log.Printf("[notify] could not open log file: %v", err)
			}
		}
	}()
}

// wait blocks until every Problem dialog has closed. Used by tests.
func (u *UI) wait() { u.wg.Wait() }

// Default is the UI the service uses.
var Default = NewUI(platformRunner(), defaultNotifier, openLogFile, time.Now)

// AskLogin asks through the Default UI.
func AskLogin(ctx context.Context, profile string) LoginChoice {
	return Default.AskLogin(ctx, profile)
}

// Problem shows a problem through the Default UI.
func Problem(tunnel, title, message string) { Default.Problem(tunnel, title, message) }

func platformRunner() DialogRunner {
	if runtime.GOOS == "darwin" {
		return OSAScriptRunner{Mode: DefaultDialogMode}
	}
	return nil
}

func openLogFile() error {
	return exec.Command("open", "-a", "Console", logfile.Path()).Run()
}
