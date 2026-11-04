package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/antero-software/lazy-ssm/awscli"
	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/ec2"
)

// Check names, in result order.
const (
	checkLogin    = "AWS login"
	checkInstance = "Instance"
	checkSSM      = "SSM"
)

const skippedMessage = "Skipped because an earlier check failed."

// Deps are the outside services a diagnosis uses.
type Deps struct {
	AWS        awscli.Runner
	LookupHost func(ctx context.Context, host string) ([]string, error)
	Profile    string // for messages only; "" shows as "default"
}

// Run checks one tunnel and returns every result. It only reads from AWS.
func Run(ctx context.Context, tc config.TunnelConfig, d Deps) Report {
	rep := Report{Tunnel: tc.Name, Description: tc.Description}

	login := loginResult(ctx, d)
	rep.Results = append(rep.Results, login)
	if login.Status == Fail {
		rep.Results = append(rep.Results, skipped(checkInstance, checkSSM)...)
		return rep
	}

	inst, instResults, found := instanceResults(ctx, tc, ec2.NewInstanceDiscoveryWithRunner(d.AWS))
	rep.Results = append(rep.Results, instResults...)

	if found {
		rep.Results = append(rep.Results, ssmResult(ctx, d.AWS, inst.ID))
	} else {
		rep.Results = append(rep.Results, skipped(checkSSM)...)
	}
	return rep
}

func loginResult(ctx context.Context, d Deps) Result {
	// The caller identity holds the user's ARN (and email). Only success or
	// failure matters, so the output is never read.
	if _, err := d.AWS.Run(ctx, "sts", "get-caller-identity", "--output", "json"); err != nil {
		profile, fix := d.Profile, "Run: aws sso login --profile "+d.Profile
		if profile == "" {
			profile, fix = "default", "Run: aws sso login"
		}
		return Result{
			Check:   checkLogin,
			Status:  Fail,
			Message: fmt.Sprintf("AWS login has expired for profile '%s'.", profile),
			Fix:     fix,
		}
	}
	return Result{Check: checkLogin, Status: Pass}
}

// instanceResults finds the instance the tunnel would use. found is false
// when there is no usable instance to check further.
func instanceResults(ctx context.Context, tc config.TunnelConfig, disc *ec2.InstanceDiscovery) (ec2.Instance, []Result, bool) {
	if tc.InstanceID != "" {
		inst, err := disc.DescribeInstance(ctx, tc.InstanceID)
		switch {
		case errors.Is(err, ec2.ErrInstanceNotFound):
			return ec2.Instance{}, []Result{{
				Check:   checkInstance,
				Status:  Fail,
				Message: fmt.Sprintf("Instance %s was not found.", tc.InstanceID),
				Fix:     "Check instance_id in config.yaml.",
			}}, false
		case err != nil:
			return ec2.Instance{}, []Result{awsFailure(checkInstance, err, "ec2", "describe-instances")}, false
		case inst.State != "running":
			return ec2.Instance{}, []Result{{
				Check:   checkInstance,
				Status:  Fail,
				Message: fmt.Sprintf("Instance %s is not running (state: %s).", inst.ID, inst.State),
			}}, false
		}
		return inst, []Result{usingResult(inst)}, true
	}

	patternFix := "Check instance_pattern in config.yaml."
	candidates, err := disc.ListCandidates(ctx, tc.InstancePattern)
	if err != nil {
		return ec2.Instance{}, []Result{awsFailure(checkInstance, err, "ec2", "describe-instances")}, false
	}
	if len(candidates) == 0 {
		return ec2.Instance{}, []Result{{
			Check:   checkInstance,
			Status:  Fail,
			Message: fmt.Sprintf("No running instance matches '%s'.", tc.InstancePattern),
			Fix:     patternFix,
		}}, false
	}
	chosen, eligible, ok := ec2.PickSSMCandidate(candidates)
	if !ok {
		return ec2.Instance{}, []Result{{
			Check:  checkInstance,
			Status: Fail,
			Message: fmt.Sprintf("%d running instances match '%s', but none has an IAM instance profile, so SSM cannot use them.",
				len(candidates), tc.InstancePattern),
			Fix: patternFix,
		}}, false
	}

	results := []Result{usingResult(chosen)}
	if eligible > 1 {
		results = append(results, Result{
			Check:  checkInstance,
			Status: Warn,
			Message: fmt.Sprintf("%d instances match '%s'. lazy-ssm used '%s'. Matches: %s",
				eligible, tc.InstancePattern, chosen.Name, ec2.FormatEligible(candidates)),
			Fix: patternFix,
		})
	}
	return chosen, results, true
}

func usingResult(inst ec2.Instance) Result {
	return Result{
		Check:   checkInstance,
		Status:  Pass,
		Message: fmt.Sprintf("Using '%s' (%s, %s).", inst.Name, inst.ID, inst.VPCID),
	}
}

func ssmResult(ctx context.Context, aws awscli.Runner, instanceID string) Result {
	out, err := aws.Run(ctx, "ssm", "describe-instance-information",
		"--filters", "Key=InstanceIds,Values="+instanceID,
		"--output", "json",
		"--query", "InstanceInformationList[0].PingStatus",
	)
	if err != nil {
		return awsFailure(checkSSM, err, "ssm", "describe-instance-information")
	}
	var status *string
	if err := json.Unmarshal(out, &status); err != nil {
		return Result{Check: checkSSM, Status: CouldNotCheck, Message: "AWS call failed: ssm describe-instance-information."}
	}

	fix := "Check the SSM agent and the instance IAM role."
	switch {
	case status == nil:
		return Result{Check: checkSSM, Status: Fail, Fix: fix,
			Message: fmt.Sprintf("Instance %s is running but is not registered with SSM.", instanceID)}
	case *status != "Online":
		return Result{Check: checkSSM, Status: Fail, Fix: fix,
			Message: fmt.Sprintf("Instance %s is running but SSM cannot reach it (status: %s).", instanceID, *status)}
	}
	return Result{Check: checkSSM, Status: Pass}
}

// awsFailure turns a failed AWS call into a CouldNotCheck result. It never
// includes AWS stderr, which can contain the caller's ARN and email.
func awsFailure(check string, err error, service, operation string) Result {
	msg := fmt.Sprintf("AWS call failed: %s %s.", service, operation)
	if awscli.IsAccessDenied(err) {
		msg = fmt.Sprintf("Your AWS role is not allowed to run %s %s.", service, operation)
	}
	return Result{Check: check, Status: CouldNotCheck, Message: msg}
}

func skipped(checks ...string) []Result {
	out := make([]Result, 0, len(checks))
	for _, c := range checks {
		out = append(out, Result{Check: c, Status: CouldNotCheck, Message: skippedMessage})
	}
	return out
}
