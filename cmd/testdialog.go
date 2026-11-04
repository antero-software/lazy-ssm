package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/antero-software/lazy-ssm/notify"
	"github.com/spf13/cobra"
)

var testDialogMode string

// testDialogCmd shows one dialog so we can check, from a real launchd agent,
// which mode brings the window in front of other apps.
var testDialogCmd = &cobra.Command{
	Use:    "test-dialog",
	Short:  "Show a test pop-up dialog",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   cmdTestDialog,
}

func init() {
	testDialogCmd.Flags().StringVar(&testDialogMode, "mode", string(notify.DefaultDialogMode), "Dialog mode: plain, activate or system-events")
}

func cmdTestDialog(_ *cobra.Command, _ []string) error {
	mode := notify.DialogMode(testDialogMode)
	switch mode {
	case notify.ModePlain, notify.ModeActivate, notify.ModeSystemEvents:
	default:
		return fmt.Errorf("unknown mode %q: use plain, activate or system-events", testDialogMode)
	}

	runner := notify.OSAScriptRunner{Mode: mode}
	res, err := runner.Show(context.Background(), notify.DialogSpec{
		Title:       "lazy-ssm: dialog test",
		Message:     "If you can read this in front of your other windows, this mode works.",
		Buttons:     []string{"Show logs", "OK"},
		Default:     "OK",
		GiveUpAfter: 60 * time.Second,
	})
	if err != nil {
		fmt.Printf("mode=%s error: %v\n", mode, err)
		return err
	}
	fmt.Printf("mode=%s result: %+v\n", mode, res)
	return nil
}
