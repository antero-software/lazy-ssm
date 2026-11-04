# Design: Pop-up Dialogs, Ask-First AWS Login, and Connection Diagnosis

**Date:** 2026-11-03
**Status:** Approved
**Replaces parts of:** `2026-03-31-user-notifications-design.md` (macOS notification delivery and the SSO flow)

## Why

lazy-ssm runs as a background Homebrew service (`sh.brew.lazy-ssm`, a launchd agent). On macOS its notifications go through `terminal-notifier` or `osascript display notification`. The user never sees them. The service is not an app bundle, so it has no notification permission of its own, and Focus mode or banner settings hide what does get through.

This causes two problems:

1. **AWS login.** When the SSO session expires, lazy-ssm sends an invisible notification and then runs `aws sso login` at once. A browser window opens with no warning and no choice.
2. **Failed connections.** Sometimes the tunnel config has the wrong instance name. lazy-ssm then picks an instance in a different VPC that cannot reach the database. The SSM session starts correctly, so lazy-ssm thinks all is well. The SQL client only shows a vague error such as "connection lost". This does not happen often, but when it does it is very frustrating, because nothing says where or why it failed.

## What success looks like

- When the user must act, a real window appears on screen and they see it.
- AWS login asks first. The browser opens only when the user clicks **Log in**.
- When a connection fails, one pop-up says which step failed and what to change.

Example (the user approved this layout):

> **Staging DB: cannot connect**
> Instance `bastion-prod-1` (vpc-111) is not in the same VPC as the database (vpc-222).
> Check `instance_pattern` in `config.yaml`.
> [Show logs] [OK]

## Goals

1. On macOS, replace Notification Centre with modal dialogs (real windows) for events the user must act on.
2. Ask before AWS SSO login, with **Later** and **Log in** buttons.
3. When a tunnel fails, run read-only checks automatically and show the first failed check with a fix.
4. Add a `lazy-ssm diagnose` command that runs the same checks in the terminal.

## Non-goals

- Pop-up dialogs on Linux. Linux keeps the current `notify-send` behaviour.
- A menu bar app, a custom toast window, or an app bundle. (This may come later as a separate project.)
- Running commands on the EC2 instance (for example SSM `send-command` with `nc`). This needs more access on servers and is out of scope.
- Changing the config or AWS resources automatically.
- Checks for network ACLs, route tables, VPC peering or transit gateways.

## Anti-goals (failure even if it technically works)

- **Pop-up spam.** More than one pop-up for one problem, or pop-ups for information the user does not need to act on.
- **False alarms.** A pop-up for a normal event, such as an idle tunnel being closed (see "Known bug to fix" below).
- **Surprise browser windows.** The browser opens without the user clicking **Log in**.
- **Secrets on screen.** Tokens, keys or credentials in a dialog or the log.
- **AWS changes.** Any diagnosis call that writes to AWS.
- **Slower healthy connections.** Diagnosis runs only after a failure, never in the normal connect path.

## Constraints

- Go, no cgo. Keep using the `aws` CLI (`aws.cli_path`) for all AWS calls, as the rest of the code does. No new AWS SDK dependency.
- macOS dialogs use `osascript`. Pass all message text as arguments (`on run argv`), never by putting text inside the script source. The current code uses Go `%q`, which is not correct AppleScript escaping, and error text can contain quotes.
- The service runs as a launchd agent in the user's GUI (Aqua) session. **The first build step is a test**, with the real service, that a dialog from that context appears in front of other windows. Prefer a way that needs no new macOS permission.
- **If no permission-free way works (user's decision):** use `tell application "System Events"` to show the dialog, and accept the one-time macOS Automation prompt ("lazy-ssm wants to control System Events"). The README must explain the prompt, why it is needed, and how to remove it (System Settings > Privacy & Security > Automation). Only request Automation of System Events. Never request Accessibility. Also confirm that the permission prompt itself appears when it comes from the background service.
- Supported macOS versions: 26 (the user's current version) and 15 (my call).
- `osascript display dialog` blocks until a click or a time-out. Dialog calls must not block unrelated tunnels or the accept loop.
- If a dialog cannot be shown (no GUI session, `osascript` fails), fall back to today's behaviour and log it. For SSO this means: log, then run `aws sso login` as today.
- All AWS calls in diagnosis are read-only and have a time-out. `AccessDenied` (or any other error) makes that check "could not check", and the other checks continue.

## Behaviour

### 1. Dialog types (macOS)

| Dialog | Title | Buttons | Default | Closes by itself after |
|---|---|---|---|---|
| Login | `lazy-ssm` | Later, Log in | Log in | 2 minutes (counts as Later) |
| Problem | `<tunnel description>: cannot connect` | Show logs, OK | OK | 10 minutes (builder's call) |

- **Show logs** opens the log file in the Console app: `open -a Console <path>`. Use the same path logic as `logFilePath()` in `cmd/tail.go` (move it to a shared place).
- Use the caution icon for both.

### 2. When a pop-up is shown

Only for events the user must act on:

| Event | Pop-up |
|---|---|
| SSO session expired | Login dialog |
| Tunnel failed to start (`ensureTunnel` error) | Run diagnosis, then Problem dialog |
| Client connection fails because the instance cannot reach the database (see 4) | Run diagnosis, then Problem dialog |
| Tunnel cannot listen on its local port (`manager.go`, for example port in use) | Problem dialog with the error and the port. No diagnosis. |
| Other SSM output lines that contain "error" | Log only (my call: today these send a notification) |
| Tunnel started, tunnel closed on idle, normal shutdown | Log only |

Linux keeps today's calls to `notify-send` for the same events.

### 3. Rules against spam

- At most one Problem dialog per tunnel on screen at a time. If one is open, new problems for that tunnel go to the log only.
- After the user closes a Problem dialog, the same finding for the same tunnel does not show again for 2 minutes (my call). A different finding can show.
- At most one Login dialog per AWS profile on screen at a time, even if many tunnels use that profile.
- Diagnosis runs at most once at a time per tunnel. If it is running, a new failure for that tunnel waits for that result and does not start a second run.

### 4. Ask-first AWS login

Changes to `sso.CheckAndLogin`:

1. `needsAuthentication` finds that the profile has expired (no change).
2. Instead of `notify.SSO` followed by `initiateLogin`, show the Login dialog: "AWS login needed for profile '`<profile>`'."
3. **Log in**: run `aws sso login --profile <profile>` (as today). Put a time-out on it (5 minutes, builder's call). The tunnel continues when it succeeds.
4. **Later**, or no click in 2 minutes: return an error. Do not ask again for that profile for 5 minutes. During those 5 minutes, connections that need the profile fail at once with a log line and no pop-up.
5. If several tunnels need the same profile at the same time, they share one dialog and one login. All of them wait for the same result.
6. After a good login, show nothing (my call). The browser shows its own success page. The SQL client has probably timed out, so the user connects again.

The SSO start URL no longer needs to go to the notifier. Keep it for the log.

### 5. Detecting the "cannot reach the database" failure

With `AWS-StartPortForwardingSessionToRemoteHost`, the local port opens even when the instance cannot reach the database. The readiness check in `ensureTunnel` passes, and the failure shows only when a client connects.

The builder must find a reliable way to detect this. Starting points:

- The Session Manager plugin prints a line when the remote connection fails. It is believed to be similar to "Connection to destination port failed, check SSM Agent logs." Confirm the exact text, and whether it goes to stdout or stderr. Today only stderr is scanned.
- As a backup signal: a client connection that closes within a few seconds with no bytes from the database side.

When detected, trigger diagnosis for that tunnel (with the rules in section 3).

### 6. Diagnosis checks

The checks run in this order. Each gives a result: **pass**, **fail** (with a plain message and a fix), **warning**, or **could not check**. Later checks that depend on an earlier result are skipped if that result failed.

| # | Check | AWS call (read-only) | Fail message (example) |
|---|---|---|---|
| 1 | AWS login | `sts get-caller-identity` | "AWS login has expired for profile `staging`." |
| 2 | Instance found | `ec2 describe-instances` (by ID, or by `tag:Name` pattern and `running`) | "No running instance matches `bastion-stg-*`." |
| 2b | More than one match | same call | Warning: "3 instances match `bastion-*`. lazy-ssm used `bastion-prod-1`." List names, IDs and VPCs. |
| 3 | SSM online | `ssm describe-instance-information` | "Instance `i-0abc` is running but SSM cannot reach it (status: ConnectionLost)." |
| 4 | Database found | `rds describe-db-instances`, then `rds describe-db-clusters` (match `rds_endpoint` against instance, cluster writer, reader and custom endpoints) | If not found, the result is **could not check**, never a warning or fail. Use neutral wording: "`rds_endpoint` is not an RDS database in this account and region, so lazy-ssm used the address check instead." |
| 4b | Database running | same call | "Database `staging-db` is not running (status: stopped)." |
| 5 | Same VPC | values from 2 and 4 | "Instance `bastion-prod-1` (vpc-111) is not in the same VPC as the database (vpc-222). Check `instance_pattern` in `config.yaml`." See the cross-VPC rule below. |
| 5b | Address in instance VPC (only when 4 is could not check) | resolve `rds_endpoint` on the Mac (or use it if it is an IP), then `ec2 describe-vpcs` for the instance VPC | Warning: "Database address 10.40.210.28 is outside the instance VPC (vpc-111, 10.0.0.0/16). Check `instance_pattern` in `config.yaml`." If the name does not resolve, **could not check**. |
| 6 | Firewall (RDS only) | `ec2 describe-security-groups` for the database groups | "Database security group `sg-123` does not allow port 5432 from instance `bastion-stg-1` (sg-456, 10.0.1.5)." |

Notes:

- Check 2 must pick the instance with the same rule the tunnel uses (today: first running match with an IAM instance profile, in `ec2.FindInstanceWithSSM`). Make one shared function that returns all candidates with details (ID, Name, VPC, security groups, private IP), so the tunnel and the diagnosis always agree.
- In the tunnel, keep today's "take the first match" choice, but log a warning that lists all matches (my call: no change to which instance is used).
- **Non-RDS targets (my call).** The user's real config has Aurora clusters, a raw private IP, and an ElastiCache Redis endpoint. Checks 4b and 6 only cover RDS and Aurora. Check 5b covers the rest, so the main wrong-VPC case is still found for every target type. RDS Proxy, ElastiCache API lookups and Route 53 private zones are out of scope for now.
- **Cross-VPC rule (my call).** Some setups reach a database in another VPC on purpose (peering, Transit Gateway). So check 6 still runs when the VPCs differ:
  - VPCs differ and check 6 fails: **fail** with the VPC message (the approved example). A wrong instance is the most likely cause.
  - VPCs differ but check 6 passes: **warning** only ("The instance and database are in different VPCs. This works only if the VPCs are connected."). Continue.
- Check 6 passes if any inbound rule on the database security groups allows `rds_port` from one of the instance security groups, or from a CIDR that contains the instance private IP. Outbound rules on the instance are the builder's call.
- If every check passes, the Problem dialog says: "All checks passed. The cause may be outside what lazy-ssm can check (network ACLs, routing, database credentials)." and keeps the original error text.
- VPC IDs and names are fine to show. Never show credentials.

### 7. What the user sees from a diagnosis

- **Problem dialog:** the first **fail** result, as in the example at the top. If there is no fail but there is a warning (for example 2b), show the warning.
- **Log:** every check and its result, one line each, prefixed with the tunnel name.

### 8. `lazy-ssm diagnose` command

```
lazy-ssm diagnose [tunnel-name]
```

- Loads config with the same flags as the root command (`--config`, `--profile`, `--region`).
- With a name: checks that tunnel. Without a name: checks every tunnel (my call).
- Prints one line per check with pass, fail, warning or could not check, then the fix for any failure.
- Shows no pop-ups and does not start `aws sso login`. If check 1 fails, it prints the exact command: `aws sso login --profile <profile>`.
- Exit code 0 if nothing failed, 1 if any check failed.
- Does not need the service to be running.

## Known bug to fix as part of this work

When a tunnel is closed on idle or at shutdown, `closeTunnel` kills the SSM process. The exit goroutine in `ensureTunnel` then sees `signal: killed` and calls `notify.Error`. Today this is invisible. With dialogs it would become a false pop-up on every idle close. Stops that lazy-ssm starts itself must not create a pop-up.

`closeTunnel` and the exit goroutine also both call `cmd.Wait()` on the same process. Clean this up while fixing the above.

## Files likely to change

| File | Change |
|---|---|
| `notify/notify.go` | macOS dialogs via `osascript` with argv; Login dialog that returns the choice; Problem dialog; open-at-a-time and cooldown rules; Linux unchanged |
| `sso/auth.go` | Ask first; per-profile single login; 5-minute "Later" pause |
| `ec2/discovery.go` | Shared candidate lookup with details |
| new `diagnose/` package | The checks in section 6 |
| `tunnel/tunnel.go` | Trigger diagnosis on failure; detect "cannot reach database"; no pop-up on intentional stops |
| `tunnel/manager.go` | Problem dialog for listen errors |
| `cmd/diagnose.go`, `cmd/root.go` | New command |
| `cmd/tail.go` | Move `logFilePath()` somewhere shared |
| `README.md` | Document dialogs, `diagnose`, and the AWS read permissions diagnosis uses |

## Left to the builder

- Package layout and type names.
- How to bring the dialog to the front (within the constraint above).
- The exact detection method for the "cannot reach database" failure.
- Problem dialog time-out, `aws sso login` time-out.
- Whether to check instance outbound rules.
- Test approach. The `osascript` and `aws` calls should sit behind small interfaces so the logic can be tested without them.

## Security notes

- Diagnosis needs these read permissions: `sts:GetCallerIdentity`, `ec2:DescribeInstances`, `ec2:DescribeSecurityGroups`, `ec2:DescribeVpcs`, `ssm:DescribeInstanceInformation`, `rds:DescribeDBInstances`, `rds:DescribeDBClusters`, `rds:DescribeDBClusterEndpoints`. Missing permissions give "could not check", not an error. Client accounts may need a change request to add them, so "could not check" may be common at first. Say this in the README.
- Use `sts get-caller-identity` only as pass or fail. Never log or show the ARN, because SSO role ARNs usually contain the user's email address.
- Diagnosis writes more network detail to the log (private IPs, security group IDs, CIDRs). The Homebrew log can be readable by other local users. Say this in the README.
- Login stays inside `aws sso login`, so tokens stay in the AWS CLI cache.
- Never open a URL that comes from error text or AWS output.
- Dialog text is passed as arguments, never put inside script source.
