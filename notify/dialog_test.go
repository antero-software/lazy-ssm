package notify

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDialogArgs_TextPassesThroughUnchanged(t *testing.T) {
	msg := `He said "hi" \ back` + "\n" + `-e do shell script "x" 🙂`
	title := `Q's "DB"`
	spec := DialogSpec{
		Title:       title,
		Message:     msg,
		Buttons:     []string{"Later", "Log in"},
		Default:     "Log in",
		GiveUpAfter: 2 * time.Minute,
	}

	for _, mode := range []DialogMode{ModePlain, ModeActivate, ModeSystemEvents} {
		t.Run(string(mode), func(t *testing.T) {
			args := dialogArgs(mode, spec)
			if len(args) < 2 || args[0] != "-e" {
				t.Fatalf("args must start with -e <script>, got %q", args)
			}
			script := args[1]
			if strings.Contains(script, msg) || strings.Contains(script, title) {
				t.Errorf("script source must not contain dialog text: %q", script)
			}
			want := []string{"120", title, msg, "Log in", "Later", "Log in"}
			if got := args[2:]; !reflect.DeepEqual(got, want) {
				t.Errorf("argv = %q, want %q", got, want)
			}
		})
	}
}

func TestDialogArgs_FirstArgvIsNumeric(t *testing.T) {
	args := dialogArgs(ModePlain, DialogSpec{
		Title:       "t",
		Message:     "-starts with a dash",
		Buttons:     []string{"Show logs", "OK"},
		Default:     "OK",
		GiveUpAfter: 10 * time.Minute,
	})
	if args[2] != "600" {
		t.Errorf("first argv after script = %q, want %q", args[2], "600")
	}
}

func TestDialogArgs_ModesUseDifferentScripts(t *testing.T) {
	spec := DialogSpec{Title: "t", Message: "m", Buttons: []string{"OK"}, Default: "OK", GiveUpAfter: time.Minute}

	sysEvents := dialogArgs(ModeSystemEvents, spec)[1]
	if !strings.Contains(sysEvents, `tell application "System Events"`) {
		t.Errorf("system-events script does not tell System Events: %q", sysEvents)
	}

	plain := dialogArgs(ModePlain, spec)[1]
	if strings.Contains(plain, "System Events") {
		t.Errorf("plain script must not use System Events: %q", plain)
	}

	activate := dialogArgs(ModeActivate, spec)[1]
	if !strings.Contains(activate, "activate") || strings.Contains(activate, "System Events") {
		t.Errorf("activate script must activate and not use System Events: %q", activate)
	}
}

func TestParseDialogOutput(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    DialogResult
		wantErr bool
	}{
		{name: "button clicked", out: "Log in\nfalse\n", want: DialogResult{Button: "Log in", GaveUp: false}},
		{name: "gave up", out: "\ntrue\n", want: DialogResult{Button: "", GaveUp: true}},
		{name: "garbage", out: "garbage", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDialogOutput(tt.out)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestClassifyRunError_SignalMeansInterrupted(t *testing.T) {
	err := exec.Command("sh", "-c", "kill -INT $$").Run()
	if !errors.Is(classifyRunError(err), ErrDialogInterrupted) {
		t.Errorf("signal exit = %v, want ErrDialogInterrupted", classifyRunError(err))
	}

	err = exec.Command("sh", "-c", "exit 1").Run()
	if errors.Is(classifyRunError(err), ErrDialogInterrupted) {
		t.Error("a normal failure exit must not count as interrupted")
	}
}
