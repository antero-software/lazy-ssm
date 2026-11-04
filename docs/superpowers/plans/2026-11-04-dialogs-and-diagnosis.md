# Pop-up Dialogs, Ask-First Login and Connection Diagnosis Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On macOS, show real dialog windows (not Notification Centre) for AWS login and tunnel failures, ask before `aws sso login`, and run read-only AWS checks that say where a failed connection broke.

**Architecture:** `notify` gains an `osascript` dialog runner (text passed only as argv) and a `UI` type that applies the anti-spam rules. `sso` asks through `notify.AskLogin` behind a per-profile gate. A new `awscli` package wraps the AWS CLI so `ec2` and a new `diagnose` package can be tested with fakes. `tunnel` runs a diagnosis when a connection fails and shows the first failed check with `notify.Problem`. A new `lazy-ssm diagnose` command prints the same report.

**Tech Stack:** Go 1.25 standard library, existing `cobra` and `viper`. AWS CLI v2 and Session Manager plugin at runtime. `osascript` on macOS, `notify-send` on Linux.

**Spec:** `docs/superpowers/specs/2026-11-03-dialogs-and-diagnosis-design.md`

**Branch:** create `feat/dialogs-and-diagnosis` from `fix/tunnel-reuse-and-keepalive` (that branch has one unmerged commit, `66ed69f`, which changes `tunnel/tunnel.go`). Commit the spec and this plan as the first commit.

## Global Constraints

- No cgo. No new third-party modules. Standard library plus existing `cobra` and `viper` only.
- All AWS access goes through the AWS CLI at `cfg.AWS.CLIPath`. Diagnosis makes only read calls: `sts get-caller-identity`, `ec2 describe-instances`, `ec2 describe-security-groups`, `ec2 describe-vpcs`, `ssm describe-instance-information`, `rds describe-db-instances`, `rds describe-db-clusters`, `rds describe-db-cluster-endpoints`.
- `osascript` script sources are constants. All dialog text goes in as argv. Never build script source from data.
- Never put AWS stderr, ARNs or caller identity output into a dialog, a diagnosis message, or the log from diagnosis. AWS `AccessDenied` stderr contains the caller ARN, which contains the user's email address.
- Never open a URL that comes from error text or AWS output.
- Timeouts: login dialog gives up after 2 minutes (counts as Later); problem dialog gives up after 10 minutes; "Later" pauses login prompts for that profile for 5 minutes; same finding for the same tunnel is not shown again for 2 minutes after its dialog closes; `aws sso login` is cut off after 5 minutes; each AWS CLI call in diagnosis is cut off after 10 seconds; a whole diagnosis is cut off after 30 seconds.
- Linux keeps `notify-send` for everything. Dialogs are macOS only. Must work on macOS 26 and 15.
- User-facing text uses Australian English and contains no em or en dashes.
- Exact dialog copy:
  - Login: title `lazy-ssm`, message `AWS login needed for profile '<profile>'.`, buttons `Later`, `Log in` (default `Log in`), caution icon.
  - Problem: title `<Description or Name>: cannot connect`, buttons `Show logs`, `OK` (default `OK`), caution icon. **Show logs** runs `open -a Console <log path>`.
- Run `go test -race ./...` and `go vet ./...` before every commit.

## Review Focus

1. **Dialog text with quotes, backslashes, newlines, emoji, or a leading `-`** (error text is arbitrary). It must show exactly as written and never break or change the script. Pinned in Task 1 (`TestDialogArgs_TextPassesThroughUnchanged`, `TestDialogArgs_FirstArgvIsNumeric`).
2. **AWS `AccessDenied` on one check in a tightly scoped client account.** That check is "could not check", the rest still run, and no ARN or email address leaks into results. Pinned in Task 5 (`TestRun_AccessDeniedIsCouldNotCheckAndLeaksNothing`).
3. **No GUI session or `osascript` failure** (service started over SSH, or before login). Login must fall back to today's behaviour (notification, then `aws sso login`), problems fall back to a notification. Pinned in Task 2 (`TestAskLogin_RunnerErrorFallsBackToLoginNow`, `TestProblem_RunnerErrorFallsBackToNotification`).
4. **Idle close, config reload and shutdown** kill the SSM process on purpose. None of them may show a pop-up. Pinned in Task 7 (`TestCloseTunnel_NoProblemOnIntentionalStop`).
5. **`rds_endpoint` written differently from what RDS returns** (upper case, trailing dot, reader or custom endpoint) and **non-RDS targets** (raw IP, ElastiCache name). Pinned in Task 6 (`TestFindDatabase_*`, `TestRun_NonRDS*`).

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `logfile/logfile.go` | Create | `Path()` moved from `cmd/tail.go` |
| `notify/dialog.go` | Create | `DialogRunner`, `OSAScriptRunner`, argv builder, output parser |
| `notify/ui.go` | Create | `UI`: `AskLogin`, `Problem`, anti-spam rules, package-level wrappers |
| `notify/notify.go` | Modify | Keep `Notifier` as the Linux and fallback path |
| `cmd/testdialog.go` | Create | Hidden `lazy-ssm test-dialog --mode` for the front-window check |
| `sso/gate.go` | Create | Per-profile single login and 5-minute pause |
| `sso/auth.go` | Modify | Ask first, 5-minute login timeout |
| `awscli/awscli.go` | Create | `Runner`, `CLI`, `Error`, `IsAccessDenied` |
| `config/config.go` | Modify | `ProfileFor`, `RegionFor` |
| `ec2/discovery.go` | Modify | `Instance`, `ListCandidates`, `DescribeInstance`, `PickSSMCandidate`; remove unused `FindInstance` |
| `diagnose/report.go` | Create | `Status`, `Result`, `Report`, output format |
| `diagnose/checks.go` | Create | Checks 1 to 3 and `Run` |
| `diagnose/network.go` | Create | Checks 4 to 6 and 5b, cross-VPC rule |
| `tunnel/tunnel.go` | Modify | Intentional stop, diagnosis trigger, destination failure detection |
| `tunnel/manager.go` | Modify | Problem dialog for listen errors |
| `cmd/diagnose.go` | Create | `lazy-ssm diagnose [tunnel-name]` |
| `cmd/tail.go`, `cmd/root.go` | Modify | Use `logfile.Path()`, register commands |
| `README.md` | Modify | Pop-ups, Automation prompt, `diagnose`, IAM permissions, log contents |

Every new `.go` file has a `_test.go` beside it, in the same package.

---

### Task 1: Dialog runner and front-window check

**Files:**
- Create: `logfile/logfile.go`, `notify/dialog.go`, `notify/dialog_test.go`, `cmd/testdialog.go`
- Modify: `cmd/tail.go` (delete `logFilePath`, call `logfile.Path()`), `cmd/root.go` (register `testDialogCmd`)

**Interfaces:**
- Produces:
  ```go
  package logfile
  func Path() string // body moved unchanged from cmd/tail.go logFilePath

  package notify
  type DialogSpec struct {
      Title, Message string
      Buttons        []string // left to right, max 3
      Default        string
      GiveUpAfter    time.Duration
  }
  type DialogResult struct {
      Button string // "" when GaveUp
      GaveUp bool
  }
  type DialogRunner interface {
      Show(ctx context.Context, d DialogSpec) (DialogResult, error)
  }
  type DialogMode string
  const (
      ModePlain        DialogMode = "plain"         // display dialog
      ModeActivate     DialogMode = "activate"      // activate, then display dialog
      ModeSystemEvents DialogMode = "system-events" // tell application "System Events"
  )
  var DefaultDialogMode = ModePlain // set in Step 7
  type OSAScriptRunner struct{ Mode DialogMode }
  func (r OSAScriptRunner) Show(ctx context.Context, d DialogSpec) (DialogResult, error)
  func dialogArgs(mode DialogMode, d DialogSpec) []string // full argv for exec "osascript"
  func parseDialogOutput(out string) (DialogResult, error)
  ```

Argv order is fixed: `-e <script for mode>`, then `<give-up seconds>`, `<title>`, `<message>`, `<default button>`, `<button 1>`, `<button 2>`... The give-up seconds go first on purpose: it is always numeric, so `osascript` stops option parsing there and a message starting with `-` is safe. The script reads them with `on run argv`, uses `with icon caution giving up after`, and returns `<button returned>` + linefeed + `<gave up as text>`. Do not use `cancel button` (it makes `osascript` exit with error -128 on Later).

- [ ] **Step 1: Write the failing tests** in `notify/dialog_test.go`

  ```go
  func TestDialogArgs_TextPassesThroughUnchanged(t *testing.T)
  // Message `He said "hi" \ back` + "\n" + `-e do shell script "x" 🙂`, title `Q's "DB"`.
  // For each mode: args[0]=="-e"; args[1] (script) contains neither the message nor the title;
  // args[2:] == []string{"120", title, message, "Log in", "Later", "Log in"} with GiveUpAfter 2*time.Minute.

  func TestDialogArgs_FirstArgvIsNumeric(t *testing.T)
  // GiveUpAfter 10*time.Minute -> args[2] == "600".

  func TestDialogArgs_ModesUseDifferentScripts(t *testing.T)
  // ModeSystemEvents script contains `tell application "System Events"`; ModePlain script does not;
  // ModeActivate script contains "activate" and not "System Events".

  func TestParseDialogOutput(t *testing.T)
  // "Log in\nfalse\n" -> {Button:"Log in", GaveUp:false}
  // "\ntrue\n"        -> {Button:"",       GaveUp:true}
  // "garbage"         -> error
  ```

- [ ] **Step 2: Run them and confirm they fail**

  Run: `go test ./notify/ -run 'TestDialogArgs|TestParseDialogOutput' -v`
  Expected: FAIL, `undefined: dialogArgs`

- [ ] **Step 3: Implement `logfile.Path`, `dialogArgs`, `parseDialogOutput` and `OSAScriptRunner.Show`**

  `Show` runs `exec.CommandContext(ctx, "osascript", dialogArgs(r.Mode, d)...)`, returns stdout parsed, or an error that includes the exit status and stderr (stderr from `osascript` is safe to log; it is not AWS output). Move `logFilePath` to `logfile.Path` unchanged and update `cmd/tail.go`.

- [ ] **Step 4: Run the tests and confirm they pass**

  Run: `go test -race ./notify/ ./cmd/ -v`
  Expected: PASS

- [ ] **Step 5: Add hidden command `lazy-ssm test-dialog --mode <plain|activate|system-events>`** in `cmd/testdialog.go`

  `Hidden: true`. Shows a Problem-style dialog (title `lazy-ssm: dialog test`, message `If you can read this in front of your other windows, this mode works.`, buttons `Show logs`, `OK`, give up after 60 seconds) with the given mode and prints the `DialogResult` or error. Register it in `cmd/root.go`.

- [ ] **Step 6: HUMAN CHECK. Test each mode from a real launchd agent (spec go/no-go)**

  Build: `go build -o /tmp/lazy-ssm-dialogtest .`
  For each mode, write `/tmp/dialogtest.plist` (Label `com.antero.lazy-ssm.dialogtest`, `ProgramArguments` = `/tmp/lazy-ssm-dialogtest`, `test-dialog`, `--mode`, `<mode>`; `StandardOutPath` and `StandardErrorPath` = `/tmp/dialogtest.log`; `LimitLoadToSessionType` = `Aqua`), then:

  ```bash
  launchctl bootstrap gui/$(id -u) /tmp/dialogtest.plist
  # Ask the user: with a full-screen app in front, did the dialog appear in front? Did a permission prompt appear?
  launchctl bootout gui/$(id -u)/com.antero.lazy-ssm.dialogtest
  cat /tmp/dialogtest.log
  ```

  Expected: the user confirms one mode where the dialog appears in front. Try `plain`, then `activate`, then `system-events`. For `system-events`, the user must also see the one-time "wants to control System Events" prompt, click Allow, and then see the dialog in front.
  If no mode works, STOP and report to the user. Do not continue to Task 2.

- [ ] **Step 7: Set `DefaultDialogMode` to the first mode that worked** and add one line to the spec under Constraints: `Front-window check (YYYY-MM-DD, macOS <version>): <mode> works.`

- [ ] **Step 8: Commit**

  ```bash
  git add logfile/ notify/dialog.go notify/dialog_test.go cmd/testdialog.go cmd/tail.go cmd/root.go docs/superpowers/specs/2026-11-03-dialogs-and-diagnosis-design.md
  git commit -m "feat(notify): add osascript dialog runner and front-window test command"
  ```

---

### Task 2: Dialog UI with anti-spam rules

**Files:**
- Create: `notify/ui.go`, `notify/ui_test.go`
- Modify: `notify/notify.go` (no behaviour change; `Notifier` stays as the Linux and fallback path)

**Interfaces:**
- Consumes: `DialogRunner`, `DialogSpec`, `DialogResult`, `OSAScriptRunner`, `DefaultDialogMode` (Task 1); `logfile.Path()`; existing `*Notifier` with `Error(title, message)` and `SSO(profile, ssoURL)`.
- Produces:
  ```go
  type LoginChoice int
  const (
      LoginNow LoginChoice = iota
      LoginLater
  )
  type UI struct { /* unexported fields */ }
  // runner is nil on non-darwin. openLogs opens the log file. now is the clock.
  func NewUI(runner DialogRunner, fallback *Notifier, openLogs func() error, now func() time.Time) *UI
  func (u *UI) AskLogin(ctx context.Context, profile string) LoginChoice // blocks
  func (u *UI) Problem(tunnel, title, message string)                   // returns at once
  func (u *UI) wait()                                                     // test helper: waits for open Problem dialogs

  var Default *UI // darwin: OSAScriptRunner{Mode: DefaultDialogMode}; else nil runner
  func AskLogin(ctx context.Context, profile string) LoginChoice
  func Problem(tunnel, title, message string)
  ```

Rules (from spec section 3):
- `AskLogin`: nil runner, or runner error, logs `[notify] dialog unavailable: <err>`, calls `fallback.SSO(profile, "")`, returns `LoginNow`. Button `Log in` returns `LoginNow`. `Later` or give-up returns `LoginLater`.
- `Problem`: nil runner calls `fallback.Error(title, message)`. If a Problem dialog for `tunnel` is on screen, log and drop. If the same `tunnel` + `message` closed less than 2 minutes ago, drop. Otherwise mark open and show in a goroutine. On close, record the close time and clear the open mark. `Show logs` calls `openLogs`. Runner error calls `fallback.Error(title, message)`.

- [ ] **Step 1: Write the failing tests** in `notify/ui_test.go` with a fake `DialogRunner` (records specs, returns a set result, or blocks on a channel) and a fake `Notifier` built like `testNotifier` in `notify_test.go`

  ```go
  func TestAskLogin_DialogCopy(t *testing.T)
  // spec == DialogSpec{Title:"lazy-ssm", Message:"AWS login needed for profile 'staging'.",
  //   Buttons:[]string{"Later","Log in"}, Default:"Log in", GiveUpAfter:2*time.Minute}
  func TestAskLogin_Choices(t *testing.T)
  // "Log in" -> LoginNow; "Later" -> LoginLater; GaveUp -> LoginLater
  func TestAskLogin_RunnerErrorFallsBackToLoginNow(t *testing.T)
  // runner returns error -> LoginNow, fallback dispatch called once
  func TestAskLogin_NilRunnerFallsBackToLoginNow(t *testing.T)
  func TestProblem_DialogCopy(t *testing.T)
  // spec == DialogSpec{Title:"Staging DB: cannot connect", Message:"m",
  //   Buttons:[]string{"Show logs","OK"}, Default:"OK", GiveUpAfter:10*time.Minute}
  func TestProblem_OneOpenPerTunnel(t *testing.T)
  // runner blocks; Problem("a",...,"m1"); Problem("a",...,"m2") -> 1 Show; Problem("b",...) -> 2 Shows
  func TestProblem_SameFindingCooldown2Minutes(t *testing.T)
  // closed at T; same message at T+1m59s -> dropped; at T+2m01s -> shown; different message at T+1s -> shown
  func TestProblem_ShowLogsOpensLogs(t *testing.T)
  func TestProblem_RunnerErrorFallsBackToNotification(t *testing.T)
  func TestProblem_NilRunnerUsesNotification(t *testing.T)
  ```

- [ ] **Step 2: Run them and confirm they fail**

  Run: `go test ./notify/ -run 'TestAskLogin|TestProblem' -v`
  Expected: FAIL, `undefined: NewUI`

- [ ] **Step 3: Implement `UI`, `NewUI`, `AskLogin`, `Problem`, `wait`, and the package-level `Default`, `AskLogin`, `Problem`**

  `Default`'s `openLogs` runs `exec.Command("open", "-a", "Console", logfile.Path()).Run()`.

- [ ] **Step 4: Run the tests and confirm they pass**

  Run: `go test -race ./notify/ -v`
  Expected: PASS

- [ ] **Step 5: Commit**

  ```bash
  git add notify/
  git commit -m "feat(notify): add login and problem dialogs with anti-spam rules"
  ```

---

### Task 3: Ask before AWS SSO login

**Files:**
- Create: `sso/gate.go`, `sso/gate_test.go`
- Modify: `sso/auth.go`

**Interfaces:**
- Consumes: `notify.AskLogin`, `notify.LoginChoice`, `notify.LoginNow`, `notify.LoginLater` (Task 2).
- Produces:
  ```go
  var ErrLoginDeclined = errors.New("AWS login declined")
  var ErrLoginPaused   = errors.New("AWS login paused after Later")

  type loginGate struct { /* mu, inflight map[string]*loginCall, pausedUntil map[string]time.Time, now */ }
  func newLoginGate(now func() time.Time) *loginGate
  // do runs fn once per profile at a time. Callers that arrive while fn runs wait and get the same error.
  // If fn reports declined, do returns ErrLoginDeclined and pauses the profile for 5 minutes.
  // While paused, do returns ErrLoginPaused without calling fn.
  func (g *loginGate) do(profile string, fn func() (declined bool, err error)) error

  // AuthChecker gains: ask func(ctx, profile) notify.LoginChoice; login func(ctx, profile) error; gate *loginGate.
  // NewAuthChecker sets ask=notify.AskLogin, login=a.initiateLogin, gate=sharedGate (package var).
  func (a *AuthChecker) promptAndLogin(ctx context.Context, profile string) error
  ```

- [ ] **Step 1: Write the failing tests** in `sso/gate_test.go` (build `AuthChecker` directly with fake `ask`, `login` and `newLoginGate(fakeClock)`)

  ```go
  func TestPromptAndLogin_LoginNowRunsLoginOnce(t *testing.T)        // err nil, login calls 1
  func TestPromptAndLogin_LaterReturnsDeclined(t *testing.T)         // errors.Is(err, ErrLoginDeclined), login calls 0
  func TestPromptAndLogin_PausedFor5Minutes(t *testing.T)
  // Later at T. At T+4m59s: ErrLoginPaused, ask calls still 1. At T+5m01s: ask calls 2.
  func TestPromptAndLogin_SameProfileSharesOneDialog(t *testing.T)
  // ask blocks on a channel; 3 goroutines call; release -> ask calls 1, login calls 1, all err nil
  func TestPromptAndLogin_DifferentProfilesIndependent(t *testing.T) // Later on "a" does not pause "b"
  func TestPromptAndLogin_LoginErrorDoesNotPause(t *testing.T)
  // login returns error -> that error returned (not ErrLoginDeclined); next call asks again
  ```

- [ ] **Step 2: Run them and confirm they fail**

  Run: `go test ./sso/ -v`
  Expected: FAIL, `undefined: newLoginGate`

- [ ] **Step 3: Implement `loginGate`, `promptAndLogin`, and change `CheckAndLogin` and `initiateLogin`**

  In `CheckAndLogin`, replace `notify.SSO(profile, ssoURL)` + `a.initiateLogin(ctx, profile)` with `a.promptAndLogin(ctx, profile)`. Keep the existing log line and add the SSO start URL to it. Return `ErrLoginDeclined` and `ErrLoginPaused` wrapped with `%w` so callers can use `errors.Is`. In `initiateLogin`, wrap `ctx` with `context.WithTimeout(ctx, 5*time.Minute)`.

- [ ] **Step 4: Run the tests and confirm they pass**

  Run: `go test -race ./sso/ -v`
  Expected: PASS

- [ ] **Step 5: Commit**

  ```bash
  git add sso/
  git commit -m "feat(sso): ask before AWS SSO login, one prompt per profile, pause after Later"
  ```

---

### Task 4: AWS CLI runner, profile helpers, instance candidates

**Files:**
- Create: `awscli/awscli.go`, `awscli/awscli_test.go`, `config/config_test.go`, `ec2/discovery_test.go`
- Modify: `config/config.go`, `ec2/discovery.go`, `tunnel/tunnel.go` (use `ProfileFor`/`RegionFor`, `ListCandidates`, `PickSSMCandidate`)

**Interfaces:**
- Produces:
  ```go
  package awscli
  type Runner interface {
      Run(ctx context.Context, args ...string) ([]byte, error) // stdout only
  }
  type CLI struct {
      Path, Profile, Region string
      Timeout               time.Duration // 0 means 10s
  }
  func (c CLI) Run(ctx context.Context, args ...string) ([]byte, error) // failure returns *Error
  type Error struct {
      Args   []string
      Stderr string // may contain an ARN: never show or log in diagnosis
      Err    error
  }
  func (e *Error) Error() string        // "aws <args[0]> <args[1]>: <Err>" only. No stderr.
  func IsAccessDenied(err error) bool   // *Error whose Stderr contains "AccessDenied", "UnauthorizedOperation" or "not authorized"
  func buildArgs(profile, region string, args []string) []string // prepends --region then --profile when set, matching today's order

  package config
  func (c *Config) ProfileFor(t TunnelConfig) string // t.AWSProfile, else c.AWS.Profile
  func (c *Config) RegionFor(t TunnelConfig) string  // t.AWSRegion, else c.AWS.Region

  package ec2
  type Instance struct {
      ID, Name, VPCID, PrivateIP, State string
      SecurityGroupIDs                  []string
      HasIAMProfile                     bool
  }
  func NewInstanceDiscoveryWithRunner(r awscli.Runner) *InstanceDiscovery
  // NewInstanceDiscovery(cliPath, profile, region) keeps its signature and wraps awscli.CLI.
  func (d *InstanceDiscovery) ListCandidates(ctx context.Context, pattern string) ([]Instance, error) // running only, AWS order
  func (d *InstanceDiscovery) DescribeInstance(ctx context.Context, id string) (Instance, error)      // any state; error if not found
  func PickSSMCandidate(c []Instance) (chosen Instance, eligible int, ok bool) // first with HasIAMProfile
  func parseInstances(b []byte) ([]Instance, error)
  ```

  `ListCandidates` and `DescribeInstance` call `ec2 describe-instances --output json --query "Reservations[].Instances[].{ID:InstanceId,Name:Tags[?Key=='Name']|[0].Value,VPC:VpcId,IP:PrivateIpAddress,State:State.Name,SGs:SecurityGroups[].GroupId,Profile:IamInstanceProfile.Arn}"` with `--filters Name=tag:Name,Values=<pattern> Name=instance-state-name,Values=running` or `--instance-ids <id>`. `HasIAMProfile` is `Profile != null`. Rewrite `FindInstanceWithSSM` on top of these, keeping its error text. Delete the unused `FindInstance`.

- [ ] **Step 1: Write the failing tests**

  ```go
  func TestBuildArgs(t *testing.T)
  // ("p","r",["sts","get-caller-identity"]) -> ["--region","r","--profile","p","sts","get-caller-identity"]
  // ("","",["x"]) -> ["x"]
  func TestIsAccessDenied(t *testing.T)
  // Stderr "An error occurred (UnauthorizedOperation) when calling..." -> true; plain errors.New -> false
  func TestError_DoesNotIncludeStderr(t *testing.T)
  // Stderr "User: arn:aws:sts::1:assumed-role/X/keith@example.com is not authorized" -> Error() has no "arn:" and no "@"
  func TestProfileForRegionFor(t *testing.T) // tunnel value wins; empty falls back to global
  func TestParseInstances(t *testing.T)
  // fixture with 2 instances, one with Profile null -> 2 Instances, fields mapped, HasIAMProfile true/false
  func TestPickSSMCandidate(t *testing.T)
  // [noProfile, a, b] -> chosen a, eligible 2, ok; [noProfile] -> ok false, eligible 0; nil -> ok false
  ```

- [ ] **Step 2: Run them and confirm they fail**

  Run: `go test ./awscli/ ./config/ ./ec2/ -v`
  Expected: FAIL, undefined symbols

- [ ] **Step 3: Implement the interfaces above.** In `tunnel/tunnel.go`, replace the two copies of the profile and region fallback with `ProfileFor`/`RegionFor`. In `resolveInstanceID`, use `ListCandidates` + `PickSSMCandidate`. When `eligible > 1`, log `[<desc>] WARNING: <n> instances match '<pattern>': <name> (<id>, <vpc>), ... Using <chosen name> (<chosen id>).`

- [ ] **Step 4: Run all tests and confirm they pass**

  Run: `go test -race ./... && go vet ./...`
  Expected: PASS

- [ ] **Step 5: Commit**

  ```bash
  git add awscli/ config/ ec2/ tunnel/tunnel.go
  git commit -m "refactor: add awscli runner, profile helpers and instance candidate lookup"
  ```

---

### Task 5: Diagnosis report and checks 1 to 3

**Files:**
- Create: `diagnose/report.go`, `diagnose/report_test.go`, `diagnose/checks.go`, `diagnose/checks_test.go`

**Interfaces:**
- Consumes: `awscli.Runner`, `awscli.IsAccessDenied` (Task 4); `ec2.NewInstanceDiscoveryWithRunner`, `ListCandidates`, `DescribeInstance`, `PickSSMCandidate`, `Instance` (Task 4); `config.TunnelConfig`.
- Produces:
  ```go
  type Status int
  const ( Pass Status = iota; Warn; Fail; CouldNotCheck )
  func (s Status) String() string // "PASS", "WARN", "FAIL", "COULD NOT CHECK"
  type Result struct {
      Check   string // one of the check names below
      Status  Status
      Message string
      Fix     string // "" when none
  }
  type Report struct {
      Tunnel  string // tunnel Name
      Results []Result
  }
  func (r Report) Headline() (Result, bool) // first Fail, else first Warn, else false
  func (r Report) HasFail() bool
  func (r Report) Lines() []string          // one per result: "diagnose: <Check>: <STATUS>: <Message>" (+ " Fix: <Fix>" when set)
  func (r Report) Write(w io.Writer)        // see format below
  const AllPassedMessage = "All checks passed. The cause may be outside what lazy-ssm can check (network ACLs, routing, database credentials)."

  type Deps struct {
      AWS        awscli.Runner
      LookupHost func(ctx context.Context, host string) ([]string, error) // net.DefaultResolver.LookupHost in production
      Profile    string // for messages only; "" shows as "default"
  }
  func Run(ctx context.Context, tc config.TunnelConfig, d Deps) Report
  ```

  Check names, in result order: `AWS login`, `Instance`, `SSM`, `Database`, `Database running`, `Same VPC`, `Address in instance VPC`, `Firewall`. Task 5 adds the first three. Task 6 adds the rest inside `Run`.

  `Write` format (two-space indent, status padded to 16 characters):
  ```
  staging-db (Staging DB)
    PASS            AWS login
    FAIL            Same VPC: Instance 'bastion-prod-1' (vpc-111) is not in the same VPC as the database (vpc-222).
                    Fix: Check instance_pattern in config.yaml.
  ```

  Copy (exact):
  - Login fail: `AWS login has expired for profile '<profile>'.` Fix: `Run: aws sso login --profile <profile>` (omit `--profile` when default).
  - No match: `No running instance matches '<pattern>'.` Fix: `Check instance_pattern in config.yaml.`
  - Matches, none eligible: `<n> running instances match '<pattern>', but none has an IAM instance profile, so SSM cannot use them.` Same fix.
  - Explicit ID not found: `Instance <id> was not found.` Fix: `Check instance_id in config.yaml.` Not running: `Instance <id> is not running (state: <state>).`
  - Instance pass: `Using '<name>' (<id>, <vpc>).`
  - More than one eligible (extra `Instance` result, `Warn`): `<n> instances match '<pattern>'. lazy-ssm used '<name>'. Matches: <name> (<id>, <vpc>), ...`
  - SSM: query `ssm describe-instance-information --filters Key=InstanceIds,Values=<id> --output json --query "InstanceInformationList[0].PingStatus"`. `null` gives `Instance <id> is running but is not registered with SSM.` Not `Online` gives `Instance <id> is running but SSM cannot reach it (status: <status>).` Fix for both: `Check the SSM agent and the instance IAM role.`
  - Any check where `IsAccessDenied(err)`: `CouldNotCheck`, message `Your AWS role is not allowed to run <service> <operation>.` Any other AWS error: `CouldNotCheck`, message `AWS call failed: <service> <operation>.` Never include stderr.
  - Skipped because an earlier check failed: `CouldNotCheck`, message `Skipped because an earlier check failed.`

  Login uses `sts get-caller-identity`. Ignore its output completely.

- [ ] **Step 1: Write the failing tests.** Use a fake `awscli.Runner` keyed by `args[0]+" "+args[1]` that returns fixture JSON or an `*awscli.Error`.

  ```go
  func TestHeadline(t *testing.T)   // [Pass, Warn, Fail] -> Fail; [Pass, Warn] -> Warn; [Pass] -> false
  func TestWrite_Format(t *testing.T) // exact text above for a 2-result report
  func TestRun_LoginFailSkipsTheRest(t *testing.T)
  // sts returns error -> results[0] Fail with "AWS login has expired for profile 'staging'."; every later result CouldNotCheck
  func TestRun_NoMatchingInstance(t *testing.T)
  func TestRun_MatchesButNoneEligible(t *testing.T)
  func TestRun_MultipleEligibleWarnsAndListsMatches(t *testing.T)
  // 3 eligible -> one Warn result whose Message starts "3 instances match 'bastion-*'. lazy-ssm used 'bastion-prod-1'."
  func TestRun_ExplicitInstanceIDStopped(t *testing.T)
  func TestRun_SSMNotRegisteredAndConnectionLost(t *testing.T)
  func TestRun_AccessDeniedIsCouldNotCheckAndLeaksNothing(t *testing.T)
  // ssm returns *awscli.Error{Stderr:"User: arn:aws:sts::1:assumed-role/R/keith@example.com is not authorized..."}
  // -> SSM result CouldNotCheck; later checks still run; no Result Message or Fix contains "arn:" or "@"
  ```

- [ ] **Step 2: Run them and confirm they fail**

  Run: `go test ./diagnose/ -v`
  Expected: FAIL, undefined symbols

- [ ] **Step 3: Implement `report.go` and checks 1 to 3 in `checks.go`**

- [ ] **Step 4: Run the tests and confirm they pass**

  Run: `go test -race ./diagnose/ -v`
  Expected: PASS

- [ ] **Step 5: Commit**

  ```bash
  git add diagnose/
  git commit -m "feat(diagnose): add report and login, instance and SSM checks"
  ```

---

### Task 6: Database, VPC, address and firewall checks

**Files:**
- Create: `diagnose/network.go`, `diagnose/network_test.go`
- Modify: `diagnose/checks.go` (call the new checks from `Run`)

**Interfaces:**
- Consumes: Task 5 types and `Run`; the chosen `ec2.Instance` from check 2.
- Produces (unexported, used by `Run`):
  ```go
  type dbTarget struct {
      ID, Status, VPCID string
      SecurityGroupIDs  []string
  }
  func findDatabase(ctx context.Context, aws awscli.Runner, endpoint string) (dbTarget, bool, error)
  type sgRule struct {
      Protocol         string // "tcp", "udp", "-1"
      FromPort, ToPort int
      SourceGroupIDs   []string
      CIDRs            []string
  }
  func allowsFrom(rules []sgRule, port int, srcGroups []string, srcIP string) bool
  func ipInCIDRs(ip string, cidrs []string) bool
  ```

  `findDatabase` matches with `strings.EqualFold` after trimming a trailing `.` from both sides, in this order:
  1. `rds describe-db-instances --output json --query "DBInstances[].{ID:DBInstanceIdentifier,Addr:Endpoint.Address,Status:DBInstanceStatus,VPC:DBSubnetGroup.VpcId,SGs:VpcSecurityGroups[].VpcSecurityGroupId,Cluster:DBClusterIdentifier}"` (match `Addr`)
  2. `rds describe-db-clusters --output json --query "DBClusters[].{ID:DBClusterIdentifier,Writer:Endpoint,Reader:ReaderEndpoint,Status:Status,SGs:VpcSecurityGroups[].VpcSecurityGroupId}"` (match `Writer` or `Reader`; cluster VPC is the `VPC` of any instance from step 1 with that `Cluster`)
  3. `rds describe-db-cluster-endpoints --output json --query "DBClusterEndpoints[].{Cluster:DBClusterIdentifier,Addr:Endpoint}"` (match `Addr`, then use that cluster from step 2)

  A rule allows when protocol is `-1`, or `tcp` with `FromPort <= port <= ToPort`, and a source group is in `srcGroups` or a CIDR contains `srcIP`. Firewall reads `ec2 describe-security-groups --group-ids <db groups...> --output json --query "SecurityGroups[].IpPermissions[]"`. Address check reads `ec2 describe-vpcs --vpc-ids <vpc> --output json --query "Vpcs[0].CidrBlockAssociationSet[].CidrBlock"`.

  Logic and copy (exact):
  - **Database** not found: `CouldNotCheck`, `<endpoint> is not an RDS database in this account and region, so lazy-ssm used the address check instead.` Then skip `Database running`, `Same VPC`, `Firewall`, and run `Address in instance VPC`. Found: `Pass`, `Found '<db id>' (<vpc>).` and skip `Address in instance VPC`.
  - **Database running**: status not `available` gives `Fail`, `Database '<db id>' is not running (status: <status>).` No fix.
  - **Firewall** (RDS only): not allowed gives `Fail`, `Database security group <db sgs joined ", "> does not allow port <rds_port> from instance '<name>' (<instance sgs joined ", ">, <private ip>).` Fix: `Check instance_pattern in config.yaml, or ask for an inbound rule on the database security group.`
  - **Same VPC**: equal gives `Pass`. Different VPCs: compute the firewall result first, then:
    - firewall `Fail`: `Fail`, `Instance '<name>' (<vpc>) is not in the same VPC as the database (<db vpc>).` Fix: `Check instance_pattern in config.yaml.` (`instance_id` when the tunnel uses `instance_id`)
    - firewall `CouldNotCheck`: `Warn` with the same message and fix
    - firewall `Pass`: `Warn`, `The instance and database are in different VPCs. This works only if the VPCs are connected.`
  - **Address in instance VPC**: `rds_endpoint` is an IP, use it; else `LookupHost` and take the first IPv4. Lookup fails: `CouldNotCheck`, `Could not resolve <endpoint> on this Mac.` Outside: `Warn`, `Database address <ip> is outside the instance VPC (<vpc>, <cidrs joined ", ">).` Fix: `Check instance_pattern in config.yaml.` Inside: `Pass`.

- [ ] **Step 1: Write the failing tests**

  ```go
  func TestFindDatabase_ClusterWriterCaseAndTrailingDot(t *testing.T)
  // endpoint "STG.cluster-abc.ap-southeast-2.rds.amazonaws.com." matches writer "stg.cluster-abc.ap-southeast-2.rds.amazonaws.com"; VPC taken from member instance
  func TestFindDatabase_ReaderAndCustomEndpoint(t *testing.T)
  func TestFindDatabase_InstanceEndpoint(t *testing.T)
  func TestAllowsFrom(t *testing.T)
  // table: source SG match; CIDR 10.0.0.0/16 contains 10.0.1.5; port inside 5000-6000; protocol -1;
  // wrong port; udp only; no rules -> false
  func TestRun_VPCMismatchFirewallBlocks_FailsWithVPCMessage(t *testing.T)
  // Headline().Message == "Instance 'bastion-prod-1' (vpc-111) is not in the same VPC as the database (vpc-222)."
  func TestRun_VPCMismatchFirewallAllows_Warns(t *testing.T)
  func TestRun_VPCMismatchFirewallDenied_WarnsWithVPCMessage(t *testing.T)
  func TestRun_DatabaseStopped_Fails(t *testing.T)
  func TestRun_SameVPCFirewallBlocks_FailsOnFirewall(t *testing.T)
  func TestRun_NonRDSIPOutsideVPC_Warns(t *testing.T)
  // rds_endpoint "10.40.210.28", VPC CIDRs ["10.0.0.0/16"] -> Warn "Database address 10.40.210.28 is outside the instance VPC (vpc-111, 10.0.0.0/16)."
  func TestRun_NonRDSHostnameInsideVPC_Passes(t *testing.T)
  // ElastiCache name, LookupHost -> ["10.0.5.5"] -> Pass; Firewall result CouldNotCheck (skipped)
  func TestRun_NonRDSUnresolvable_CouldNotCheck(t *testing.T)
  func TestRun_AllPass_NoHeadline(t *testing.T)
  ```

- [ ] **Step 2: Run them and confirm they fail**

  Run: `go test ./diagnose/ -run 'TestFindDatabase|TestAllowsFrom|TestRun_' -v`
  Expected: FAIL, undefined symbols

- [ ] **Step 3: Implement `network.go` and wire it into `Run`**

- [ ] **Step 4: Run the tests and confirm they pass**

  Run: `go test -race ./diagnose/ -v`
  Expected: PASS

- [ ] **Step 5: Commit**

  ```bash
  git add diagnose/
  git commit -m "feat(diagnose): add database, VPC, address and firewall checks"
  ```

---

### Task 7: No pop-ups on intentional stops

**Files:**
- Modify: `tunnel/tunnel.go`
- Test: `tunnel/tunnel_test.go`

**Interfaces:**
- Consumes: `notify.Problem` (Task 2).
- Produces (fields and methods on `LazySSMTunnel`, used by Task 8):
  ```go
  problem func(tunnel, title, message string) // default notify.Problem
  func (t *LazySSMTunnel) startProcess(cmd *exec.Cmd) error // Start, record start time, exit watcher; caller holds t.mu
  func (t *LazySSMTunnel) displayName() string               // Description, else Name
  ```

  `closeTunnel` marks the current command as stopped on purpose, kills it, and waits on that command's exit channel (created in `startProcess`). It must not call `cmd.Wait()` a second time. The exit watcher logs `SSM tunnel stopped` for intentional stops and `ERROR: SSM tunnel process exited: <err>` otherwise. It never calls `notify` or `t.problem`. Remove the `notify.Error` calls from the exit watcher and from `scanTunnelOutput` (log only). `ensureTunnel` uses `startProcess`.

- [ ] **Step 1: Write the failing tests** (set `t.problem` to a recorder)

  ```go
  func TestCloseTunnel_NoProblemOnIntentionalStop(t *testing.T)
  // startProcess(exec.Command("sleep","60")); closeTunnel() returns within 2s; problem calls 0
  func TestUnexpectedExit_LogsOnlyAndClearsState(t *testing.T)
  // startProcess(exec.Command("sh","-c","exit 3")); wait for exit; tunnelCmd nil, tunnelStartedAt 0; problem calls 0
  func TestScanTunnelOutput_ErrorLinesDoNotPopUp(t *testing.T)
  // "An error occurred (TargetNotConnected)" -> problem calls 0
  ```

- [ ] **Step 2: Run them and confirm they fail**

  Run: `go test -race ./tunnel/ -run 'TestCloseTunnel|TestUnexpectedExit|TestScanTunnelOutput' -v`
  Expected: FAIL, `t.startProcess undefined`

- [ ] **Step 3: Implement `startProcess`, `displayName`, the exit channel and the intentional-stop mark; update `closeTunnel` and `scanTunnelOutput`**

- [ ] **Step 4: Run all tunnel tests with the race detector and confirm they pass**

  Run: `go test -race ./tunnel/ -v`
  Expected: PASS, no `DATA RACE`

- [ ] **Step 5: Commit**

  ```bash
  git add tunnel/
  git commit -m "fix(tunnel): no error notification on idle close or shutdown; single Wait per process"
  ```

---

### Task 8: Run diagnosis when a connection fails

**Files:**
- Modify: `tunnel/tunnel.go`, `tunnel/manager.go`
- Test: `tunnel/tunnel_test.go`

**Interfaces:**
- Consumes: `diagnose.Run`, `diagnose.Deps`, `diagnose.Report`, `diagnose.AllPassedMessage` (Tasks 5 and 6); `awscli.CLI`; `config.ProfileFor`/`RegionFor` (Task 4); `sso.ErrLoginDeclined`, `sso.ErrLoginPaused` (Task 3); `t.problem`, `displayName` (Task 7).
- Produces:
  ```go
  type failureTrigger int
  const (
      triggerStart     failureTrigger = iota // ensureTunnel returned an error
      triggerPlugin                          // plugin printed the destination failure line
      triggerHeuristic                       // connection closed fast with no bytes from the database
  )
  var errDestination = errors.New("the instance could not connect to the database")
  var errNoResponse  = errors.New("the database side closed the connection without a reply")

  // fields
  runDiagnosis func(ctx context.Context) diagnose.Report // default: diagnose.Run(ctx, t.config, diagnose.Deps{AWS: awscli.CLI{Path: CLIPath, Profile: ProfileFor, Region: RegionFor}, LookupHost: net.DefaultResolver.LookupHost, Profile: ProfileFor})
  diagnosing   atomic.Bool
  diagWG       sync.WaitGroup // tests wait on this

  func (t *LazySSMTunnel) reportFailure(cause error, trigger failureTrigger)
  func isDestinationFailure(line string) bool
  ```

  `reportFailure` rules:
  - `errors.Is(cause, sso.ErrLoginDeclined)` or `ErrLoginPaused`: log only.
  - `diagnosing` already true: log `diagnosis already running`, return.
  - Else, in a goroutine tracked by `diagWG`, with a 30-second context: run, log every `Report.Lines()` entry with the `[<displayName>]` prefix, then:
    - `Headline()` found: `t.problem(t.config.Name, "<displayName>: cannot connect", headline.Message + "\n\n" + headline.Fix)` (no blank lines when `Fix` is empty).
    - No headline and trigger is `triggerHeuristic`: log only.
    - No headline otherwise: `t.problem(..., diagnose.AllPassedMessage + "\n\n" + cause.Error())`.

  Wiring:
  - `handleConnection`: on `ensureTunnel` error, call `reportFailure(err, triggerStart)` in place of `notify.Error`. Track bytes copied in each direction (`copyData` reports its `io.Copy` count). When the connection ends after less than 5 seconds with zero bytes from the database side, call `reportFailure(errNoResponse, triggerHeuristic)`.
  - Scan **both** stdout and stderr of the SSM process with `scanTunnelOutput`. When `isDestinationFailure(line)`, call `reportFailure(errDestination, triggerPlugin)`. `isDestinationFailure` matches `connection to destination port failed` case-insensitively (confirm in Step 6).
  - `manager.go`: when `t.Start` returns a non-`context.Canceled` error, call `notify.Problem(t.config.Name, "<displayName>: cannot connect", fmt.Sprintf("lazy-ssm cannot listen on local port %d. Another program may be using it.\n\n%v", t.config.LocalPort, err))` in place of `notify.Error`. No diagnosis. Do this in both `Start` and `Reload`.

- [ ] **Step 1: Write the failing tests** (fake `runDiagnosis`, recorder for `t.problem`, `diagWG.Wait()` before asserting)

  ```go
  func TestReportFailure_ShowsHeadline(t *testing.T)
  // Description "Staging DB", report with Fail{Message:"m", Fix:"f"} -> one problem call: ("staging-db","Staging DB: cannot connect","m\n\nf")
  func TestReportFailure_LoginDeclinedOrPausedLogsOnly(t *testing.T)    // wrapped sso errors -> runDiagnosis calls 0, problem calls 0
  func TestReportFailure_OneDiagnosisAtATime(t *testing.T)              // runDiagnosis blocks; two calls -> runDiagnosis calls 1
  func TestReportFailure_HeuristicAllPassLogsOnly(t *testing.T)         // problem calls 0
  func TestReportFailure_StartAllPassShowsAllPassedAndCause(t *testing.T)
  // message == diagnose.AllPassedMessage + "\n\n" + cause.Error()
  func TestIsDestinationFailure(t *testing.T)
  // "Connection to destination port failed, check SSM Agent logs." -> true; "connection to destination port FAILED" -> true; "Port 15432 opened" -> false
  ```

- [ ] **Step 2: Run them and confirm they fail**

  Run: `go test ./tunnel/ -run 'TestReportFailure|TestIsDestinationFailure' -v`
  Expected: FAIL, `t.reportFailure undefined`

- [ ] **Step 3: Implement `reportFailure`, `isDestinationFailure`, the default `runDiagnosis`, and the wiring above**

- [ ] **Step 4: Run all tests and confirm they pass**

  Run: `go test -race ./... && go vet ./...`
  Expected: PASS

- [ ] **Step 5: Commit**

  ```bash
  git add tunnel/
  git commit -m "feat(tunnel): diagnose failed connections and show the first failed check"
  ```

- [ ] **Step 6: HUMAN CHECK. Confirm the plugin's destination failure line**

  Ask the user for a tunnel whose bastion works. Copy it in a scratch config with `rds_endpoint: 10.255.255.1` (not reachable) and a `local_port` that the running service does not use. Run `go run . --config <scratch config>` in the foreground, then connect: `nc -v localhost <local_port>`.
  Expected: the log shows a line from the plugin that `isDestinationFailure` matches, and a Problem dialog appears once. If the text is different, update `isDestinationFailure` and its test to the real text, then commit:

  ```bash
  git add tunnel/
  git commit -m "fix(tunnel): match the real Session Manager destination failure text"
  ```

---

### Task 9: `lazy-ssm diagnose` command

**Files:**
- Create: `cmd/diagnose.go`, `cmd/diagnose_test.go`
- Modify: `cmd/root.go` (register `diagnoseCmd`)

**Interfaces:**
- Consumes: `config.Load`, `ProfileFor`, `RegionFor`; `awscli.CLI`; `diagnose.Run`, `Deps`, `Report.Write`, `Report.HasFail`.
- Produces:
  ```go
  var diagnoseCmd = &cobra.Command{
      Use:          "diagnose [tunnel-name]",
      Short:        "Check why a tunnel cannot connect",
      Args:         cobra.MaximumNArgs(1),
      SilenceUsage: true,
      RunE:         cmdDiagnose,
  }
  func selectTunnels(cfg *config.Config, name string) ([]config.TunnelConfig, error)
  ```

  `selectTunnels` with `""` returns all tunnels. An unknown name returns the error `no tunnel named '<name>'. Tunnels: <names joined ", ">`. `cmdDiagnose` loads config with the root flags, runs each tunnel with a 30-second context, writes each report to stdout with a blank line between them, and calls `os.Exit(1)` after printing if any report `HasFail()`. It shows no dialogs and never runs `aws sso login`.

- [ ] **Step 1: Write the failing test**

  ```go
  func TestSelectTunnels(t *testing.T)
  // cfg with "a","b": "" -> both; "b" -> [b]; "x" -> error "no tunnel named 'x'. Tunnels: a, b"
  ```

- [ ] **Step 2: Run it and confirm it fails**

  Run: `go test ./cmd/ -run TestSelectTunnels -v`
  Expected: FAIL, `undefined: selectTunnels`

- [ ] **Step 3: Implement `diagnoseCmd`, `cmdDiagnose`, `selectTunnels`; register the command**

- [ ] **Step 4: Run the tests, then a real smoke run**

  Run: `go test -race ./cmd/ -v`
  Expected: PASS
  Run: `go run . diagnose <a working tunnel name>; echo "exit $?"`
  Expected: every line `PASS` or `COULD NOT CHECK`, then `exit 0`.

- [ ] **Step 5: Commit**

  ```bash
  git add cmd/
  git commit -m "feat(cmd): add lazy-ssm diagnose command"
  ```

---

### Task 10: README and end-to-end check

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update `README.md`** with these sections:
  - **Pop-ups (macOS):** when the Login and Problem dialogs appear, what each button does, the anti-spam rules in one sentence each, and that Linux still uses `notify-send`.
  - **macOS permission (only if Task 1 chose `system-events`):** the one-time "lazy-ssm wants to control System Events" prompt, why it is needed, and how to remove it (System Settings > Privacy & Security > Automation).
  - **`lazy-ssm diagnose`:** usage, example output (the `Write` format from Task 5), exit codes.
  - **AWS permissions for diagnosis:** the eight read actions from Global Constraints as an IAM policy snippet, a note that missing permissions give "COULD NOT CHECK", and that client accounts may need a change request.
  - **Log contents:** diagnosis writes private IPs, security group IDs and CIDRs to the log at `logfile.Path()`. The Homebrew log can be readable by other local users.

- [ ] **Step 2: HUMAN CHECK. End-to-end on the real service**

  Run: `go build -o lazy-ssm . && brew services stop lazy-ssm && ./lazy-ssm &` (or replace the Homebrew binary as the user prefers). Then, with the user watching:
  1. `aws sso logout`, then connect a SQL client. Expected: one Login dialog, even with two tunnels on the same profile. **Later**: no new dialog for 5 minutes. **Log in**: browser opens only after the click.
  2. Set one tunnel's `instance_pattern` to a bastion in another VPC, reload, connect. Expected: one Problem dialog with the VPC message. **Show logs** opens Console at the log file.
  3. Leave a tunnel idle past `idle_timeout`. Expected: no dialog.
  4. `lazy-ssm diagnose <that tunnel>; echo "exit $?"`. Expected: `FAIL  Same VPC` and `exit 1`.

  Restore the config and `brew services start lazy-ssm` afterwards.

- [ ] **Step 3: Commit**

  ```bash
  git add README.md
  git commit -m "docs: document pop-ups, diagnose command and AWS read permissions"
  ```
