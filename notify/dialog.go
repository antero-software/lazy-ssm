package notify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// DialogSpec describes one modal dialog window.
type DialogSpec struct {
	Title, Message string
	Buttons        []string // left to right, max 3
	Default        string
	GiveUpAfter    time.Duration
}

// DialogResult is what the user did with a dialog.
type DialogResult struct {
	Button string // "" when GaveUp
	GaveUp bool
}

// DialogRunner shows one dialog and blocks until it closes.
type DialogRunner interface {
	Show(ctx context.Context, d DialogSpec) (DialogResult, error)
}

// DialogMode selects how the dialog is brought on screen.
type DialogMode string

const (
	ModePlain        DialogMode = "plain"         // display dialog
	ModeActivate     DialogMode = "activate"      // activate, then display dialog
	ModeSystemEvents DialogMode = "system-events" // tell application "System Events"
)

// DefaultDialogMode is the mode the service uses. It is chosen by the
// front-window check (see the design spec).
var DefaultDialogMode = ModePlain

// The script sources are constants. All dialog text arrives through argv,
// so no data is ever parsed as AppleScript.
const (
	scriptReadArgs = `on run argv
	set giveUp to (item 1 of argv) as integer
	set ttl to item 2 of argv
	set msg to item 3 of argv
	set defBtn to item 4 of argv
	set btns to items 5 thru -1 of argv
`
	scriptShow   = "set r to display dialog msg with title ttl buttons btns default button defBtn with icon caution giving up after giveUp\n"
	scriptReturn = `	return (button returned of r) & linefeed & ((gave up of r) as text)
end run`

	scriptPlain        = scriptReadArgs + "\t" + scriptShow + scriptReturn
	scriptActivate     = scriptReadArgs + "\tactivate\n\t" + scriptShow + scriptReturn
	scriptSystemEvents = scriptReadArgs + "\ttell application \"System Events\"\n\t\tactivate\n\t\t" + scriptShow + "\tend tell\n" + scriptReturn
)

func scriptFor(mode DialogMode) string {
	switch mode {
	case ModeActivate:
		return scriptActivate
	case ModeSystemEvents:
		return scriptSystemEvents
	default:
		return scriptPlain
	}
}

// dialogArgs returns the full argv for exec "osascript". The give-up seconds
// come first: they are always numeric, so osascript stops option parsing
// there and a message that starts with "-" is never read as a flag.
func dialogArgs(mode DialogMode, d DialogSpec) []string {
	args := []string{
		"-e", scriptFor(mode),
		strconv.Itoa(int(d.GiveUpAfter / time.Second)),
		d.Title,
		d.Message,
		d.Default,
	}
	return append(args, d.Buttons...)
}

// parseDialogOutput reads "<button returned>\n<gave up>\n" from osascript.
func parseDialogOutput(out string) (DialogResult, error) {
	out = strings.TrimSuffix(out, "\n")
	i := strings.LastIndex(out, "\n")
	if i < 0 {
		return DialogResult{}, fmt.Errorf("unexpected dialog output %q", out)
	}
	button, gaveUp := out[:i], out[i+1:]
	switch gaveUp {
	case "true":
		return DialogResult{Button: "", GaveUp: true}, nil
	case "false":
		return DialogResult{Button: button}, nil
	default:
		return DialogResult{}, fmt.Errorf("unexpected dialog output %q", out)
	}
}

// OSAScriptRunner shows dialogs with osascript on macOS.
type OSAScriptRunner struct{ Mode DialogMode }

// Show displays the dialog and waits for a click or the give-up time.
func (r OSAScriptRunner) Show(ctx context.Context, d DialogSpec) (DialogResult, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "osascript", dialogArgs(r.Mode, d)...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return DialogResult{}, fmt.Errorf("osascript dialog (%s): %w: %s", r.Mode, err, strings.TrimSpace(stderr.String()))
	}
	return parseDialogOutput(stdout.String())
}
