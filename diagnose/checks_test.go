package diagnose

import (
	"context"
	"strings"
	"testing"
)

func TestRun_LoginFailSkipsTheRest(t *testing.T) {
	aws := newFakeAWS().fail("sts get-caller-identity", expiredLogin())

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	first := r.Results[0]
	if first.Check != "AWS login" || first.Status != Fail || first.Message != "AWS login has expired for profile 'staging'." {
		t.Fatalf("first result = %+v", first)
	}
	if first.Fix != "Run: aws sso login --profile staging" {
		t.Errorf("fix = %q", first.Fix)
	}
	if len(r.Results) < 3 {
		t.Fatalf("want skipped results for later checks, got %+v", r.Results)
	}
	for _, res := range r.Results[1:] {
		if res.Status != CouldNotCheck || res.Message != "Skipped because an earlier check failed." {
			t.Errorf("later result = %+v, want skipped", res)
		}
	}
	if len(aws.calls) != 1 {
		t.Errorf("AWS calls after a failed login = %q, want only sts", aws.calls)
	}
}

func TestRun_LoginFailDefaultProfileFix(t *testing.T) {
	aws := newFakeAWS().fail("sts get-caller-identity", expiredLogin())
	d := deps(aws)
	d.Profile = ""

	r := Run(context.Background(), stagingTunnel(), d)

	if r.Results[0].Message != "AWS login has expired for profile 'default'." || r.Results[0].Fix != "Run: aws sso login" {
		t.Errorf("result = %+v", r.Results[0])
	}
}

func TestRun_NoMatchingInstance(t *testing.T) {
	aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).on("ec2 describe-instances", `[]`)

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	inst, _ := resultFor(r, "Instance")
	if inst.Status != Fail || inst.Message != "No running instance matches 'bastion-stg-*'." || inst.Fix != "Check instance_pattern in config.yaml." {
		t.Errorf("Instance = %+v", inst)
	}
	if ssm, _ := resultFor(r, "SSM"); ssm.Status != CouldNotCheck || ssm.Message != "Skipped because an earlier check failed." {
		t.Errorf("SSM = %+v, want skipped", ssm)
	}
}

func TestRun_MatchesButNoneEligible(t *testing.T) {
	aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).on("ec2 describe-instances",
		`[{"ID":"i-1","Name":"bastion-stg-1","VPC":"vpc-111","State":"running","SGs":[],"Profile":null},
		  {"ID":"i-2","Name":"bastion-stg-2","VPC":"vpc-111","State":"running","SGs":[],"Profile":null}]`)

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	inst, _ := resultFor(r, "Instance")
	want := "2 running instances match 'bastion-stg-*', but none has an IAM instance profile, so SSM cannot use them."
	if inst.Status != Fail || inst.Message != want || inst.Fix != "Check instance_pattern in config.yaml." {
		t.Errorf("Instance = %+v", inst)
	}
}

func TestRun_MultipleEligibleWarnsAndListsMatches(t *testing.T) {
	aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).on("ssm describe-instance-information", ssmOnline).
		on("ec2 describe-instances", `[
		  {"ID":"i-1","Name":"bastion-prod-1","VPC":"vpc-111","State":"running","SGs":[],"Profile":"p"},
		  {"ID":"i-2","Name":"bastion-stg-1","VPC":"vpc-222","State":"running","SGs":[],"Profile":"p"},
		  {"ID":"i-3","Name":"bastion-dev-1","VPC":"vpc-333","State":"running","SGs":[],"Profile":"p"}]`)
	tc := stagingTunnel()
	tc.InstancePattern = "bastion-*"

	r := Run(context.Background(), tc, deps(aws))

	var warns []Result
	for _, res := range r.Results {
		if res.Check == "Instance" && res.Status == Warn {
			warns = append(warns, res)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("Instance warnings = %+v, want 1", warns)
	}
	wantPrefix := "3 instances match 'bastion-*'. lazy-ssm used 'bastion-prod-1'."
	if !strings.HasPrefix(warns[0].Message, wantPrefix) {
		t.Errorf("warning = %q, want prefix %q", warns[0].Message, wantPrefix)
	}
	if !strings.Contains(warns[0].Message, "bastion-stg-1 (i-2, vpc-222)") {
		t.Errorf("warning does not list matches: %q", warns[0].Message)
	}
	if pass, _ := resultFor(r, "Instance"); pass.Status != Pass || pass.Message != "Using 'bastion-prod-1' (i-1, vpc-111)." {
		t.Errorf("first Instance result = %+v", pass)
	}
}

func TestRun_ExplicitInstanceIDStopped(t *testing.T) {
	aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).on("ec2 describe-instances",
		`[{"ID":"i-0abc","Name":"bastion","VPC":"vpc-111","State":"stopped","SGs":[],"Profile":"p"}]`)
	tc := stagingTunnel()
	tc.InstancePattern = ""
	tc.InstanceID = "i-0abc"

	r := Run(context.Background(), tc, deps(aws))

	inst, _ := resultFor(r, "Instance")
	if inst.Status != Fail || inst.Message != "Instance i-0abc is not running (state: stopped)." {
		t.Errorf("Instance = %+v", inst)
	}
}

func TestRun_ExplicitInstanceIDNotFound(t *testing.T) {
	aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).on("ec2 describe-instances", `[]`)
	tc := stagingTunnel()
	tc.InstancePattern = ""
	tc.InstanceID = "i-0abc"

	r := Run(context.Background(), tc, deps(aws))

	inst, _ := resultFor(r, "Instance")
	if inst.Status != Fail || inst.Message != "Instance i-0abc was not found." || inst.Fix != "Check instance_id in config.yaml." {
		t.Errorf("Instance = %+v", inst)
	}
}

func TestRun_SSMNotRegisteredAndConnectionLost(t *testing.T) {
	tests := []struct {
		name, out, want string
	}{
		{"not registered", `null`, "Instance i-0abc is running but is not registered with SSM."},
		{"connection lost", `"ConnectionLost"`, "Instance i-0abc is running but SSM cannot reach it (status: ConnectionLost)."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).on("ec2 describe-instances", oneBastion).
				on("ssm describe-instance-information", tt.out)

			r := Run(context.Background(), stagingTunnel(), deps(aws))

			ssm, _ := resultFor(r, "SSM")
			if ssm.Status != Fail || ssm.Message != tt.want || ssm.Fix != "Check the SSM agent and the instance IAM role." {
				t.Errorf("SSM = %+v", ssm)
			}
		})
	}
}

func TestRun_SSMOnlinePasses(t *testing.T) {
	aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).on("ec2 describe-instances", oneBastion).
		on("ssm describe-instance-information", ssmOnline)

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	if ssm, _ := resultFor(r, "SSM"); ssm.Status != Pass {
		t.Errorf("SSM = %+v, want Pass", ssm)
	}
	if login, _ := resultFor(r, "AWS login"); login.Status != Pass || login.Message != "" {
		t.Errorf("AWS login = %+v, want Pass with no message", login)
	}
}

func TestRun_AccessDeniedIsCouldNotCheckAndLeaksNothing(t *testing.T) {
	aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).on("ec2 describe-instances", oneBastion).
		fail("ssm describe-instance-information", accessDenied("ssm", "describe-instance-information"))

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	ssm, _ := resultFor(r, "SSM")
	if ssm.Status != CouldNotCheck || ssm.Message != "Your AWS role is not allowed to run ssm describe-instance-information." {
		t.Errorf("SSM = %+v", ssm)
	}
	ssmIndex := -1
	for i, res := range r.Results {
		if res.Check == "SSM" {
			ssmIndex = i
		}
		for _, text := range []string{res.Message, res.Fix} {
			if strings.Contains(text, "arn:") || strings.Contains(text, "@") {
				t.Errorf("result %q leaks caller identity: %q", res.Check, text)
			}
		}
	}
	for _, line := range r.Lines() {
		if strings.Contains(line, "arn:") || strings.Contains(line, "@") {
			t.Errorf("log line leaks caller identity: %q", line)
		}
	}
	if ssmIndex == len(r.Results)-1 {
		t.Log("no checks after SSM yet (added in Task 6)")
	}
}

func TestRun_OtherAWSErrorIsCouldNotCheck(t *testing.T) {
	aws := newFakeAWS().on("sts get-caller-identity", callerIdentity).
		fail("ec2 describe-instances", expiredLogin())

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	if inst, _ := resultFor(r, "Instance"); inst.Status != CouldNotCheck || inst.Message != "AWS call failed: ec2 describe-instances." {
		t.Errorf("Instance = %+v", inst)
	}
}
