package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antero-software/lazy-ssm/awscli"
	"github.com/antero-software/lazy-ssm/diagnose"
	"github.com/antero-software/lazy-ssm/sso"
)

const (
	// diagnosisTimeout bounds one whole diagnosis.
	diagnosisTimeout = 30 * time.Second
	// diagnosisCooldown stops a burst of failed connections (a SQL client
	// retrying, or a connection pool) from re-running the AWS checks.
	diagnosisCooldown = time.Minute
	// noResponseWindow: a connection that closes this fast with no bytes
	// from the database side probably never reached the database.
	noResponseWindow = 5 * time.Second
)

// failureTrigger says how a failure was noticed.
type failureTrigger int

const (
	triggerStart     failureTrigger = iota // ensureTunnel returned an error
	triggerPlugin                          // plugin printed the destination failure line
	triggerHeuristic                       // connection closed fast with no bytes from the database
)

var (
	errDestination = errors.New("the instance could not connect to the database")
	errNoResponse  = errors.New("the database side closed the connection without a reply")
)

// isDestinationFailure reports whether a Session Manager plugin output line
// says the instance could not open the connection to the database.
func isDestinationFailure(line string) bool {
	return strings.Contains(strings.ToLower(line), "connection to destination port failed")
}

// noResponse reports whether a finished connection looks like it never
// reached the database.
func noResponse(bytesFromDB int64, lasted time.Duration) bool {
	return bytesFromDB == 0 && lasted < noResponseWindow
}

// defaultDiagnosis runs the real read-only AWS checks for this tunnel.
func (t *LazySSMTunnel) defaultDiagnosis(ctx context.Context) diagnose.Report {
	profile := t.appConfig.ProfileFor(t.config)
	return diagnose.Run(ctx, t.config, diagnose.Deps{
		AWS:        awscli.CLI{Path: t.appConfig.AWS.CLIPath, Profile: profile, Region: t.appConfig.RegionFor(t.config)},
		LookupHost: net.DefaultResolver.LookupHost,
		Profile:    profile,
	})
}

// reportFailure runs a diagnosis in the background and shows the first
// failed check in a pop-up. Only one diagnosis runs per tunnel at a time, and
// none runs again within a minute of the last one.
func (t *LazySSMTunnel) reportFailure(cause error, trigger failureTrigger) {
	name := t.displayName()
	if errors.Is(cause, sso.ErrLoginDeclined) || errors.Is(cause, sso.ErrLoginPaused) {
		log.Printf("[%s] Not diagnosing: %v", name, cause)
		return
	}
	if last := t.lastDiagnosis.Load(); last != 0 && time.Since(time.Unix(0, last)) < diagnosisCooldown {
		log.Printf("[%s] Diagnosed less than %v ago, not running again: %v", name, diagnosisCooldown, cause)
		return
	}
	if !t.diagnosing.CompareAndSwap(false, true) {
		if trigger != triggerHeuristic {
			// Remember a clear failure seen while a quiet one is being checked.
			t.diagMu.Lock()
			t.strongCause = cause
			t.diagMu.Unlock()
		}
		log.Printf("[%s] Diagnosis already running: %v", name, cause)
		return
	}
	t.diagMu.Lock()
	t.strongCause = nil
	t.diagMu.Unlock()

	t.diagWG.Add(1)
	go func() {
		defer t.diagWG.Done()
		defer func() {
			t.lastDiagnosis.Store(time.Now().UnixNano())
			t.diagnosing.Store(false)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), diagnosisTimeout)
		defer cancel()
		log.Printf("[%s] Connection failed (%v). Running diagnosis...", name, cause)
		rep := t.runDiagnosis(ctx)
		for _, line := range rep.Lines() {
			log.Printf("[%s] %s", name, line)
		}

		title := fmt.Sprintf("%s: cannot connect", name)
		if head, ok := rep.Headline(); ok {
			msg := head.Message
			if head.Fix != "" {
				msg += "\n\n" + head.Fix
			}
			t.problem(t.config.Name, title, msg)
			return
		}

		if trigger == triggerHeuristic {
			t.diagMu.Lock()
			strong := t.strongCause
			t.diagMu.Unlock()
			if strong == nil {
				log.Printf("[%s] All checks passed for a connection that closed early; no pop-up.", name)
				return
			}
			cause = strong
		}
		t.problem(t.config.Name, title, diagnose.AllPassedMessage+"\n\n"+cause.Error())
	}()
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
