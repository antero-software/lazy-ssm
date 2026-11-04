package diagnose

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/antero-software/lazy-ssm/awscli"
	"github.com/antero-software/lazy-ssm/config"
)

// fakeAWS answers AWS CLI calls by "<service> <operation>".
type fakeAWS struct {
	mu        sync.Mutex
	responses map[string]string
	errs      map[string]error
	calls     []string
}

func newFakeAWS() *fakeAWS {
	return &fakeAWS{responses: map[string]string{}, errs: map[string]error{}}
}

func (f *fakeAWS) on(key, out string) *fakeAWS { f.responses[key] = out; return f }

func (f *fakeAWS) fail(key string, err error) *fakeAWS { f.errs[key] = err; return f }

func (f *fakeAWS) Run(_ context.Context, args ...string) ([]byte, error) {
	key := args[0] + " " + args[1]
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(args, " "))
	f.mu.Unlock()
	if err, ok := f.errs[key]; ok {
		return nil, err
	}
	if out, ok := f.responses[key]; ok {
		return []byte(out), nil
	}
	return nil, &awscli.Error{Args: args, Stderr: "unexpected call in test", Err: errors.New("exit status 255")}
}

func accessDenied(service, op string) error {
	return &awscli.Error{
		Args:   []string{service, op},
		Stderr: "An error occurred (AccessDenied) when calling the operation: User: arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_Dev/keith@example.com is not authorized to perform this action",
		Err:    errors.New("exit status 254"),
	}
}

func expiredLogin() error {
	return &awscli.Error{
		Args:   []string{"sts", "get-caller-identity"},
		Stderr: "Error when retrieving token from sso: Token has expired and refresh failed",
		Err:    errors.New("exit status 255"),
	}
}

const (
	callerIdentity = `{"UserId":"AROA:keith@example.com","Account":"123456789012","Arn":"arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_Dev/keith@example.com"}`
	oneBastion     = `[{"ID":"i-0abc","Name":"bastion-stg-1","VPC":"vpc-111","IP":"10.0.1.5","State":"running","SGs":["sg-456"],"Profile":"arn:aws:iam::1:instance-profile/ssm"}]`
	ssmOnline      = `"Online"`
)

func stagingTunnel() config.TunnelConfig {
	return config.TunnelConfig{
		Name:            "staging-db",
		Description:     "Staging DB",
		InstancePattern: "bastion-stg-*",
		RDSEndpoint:     "stg.cluster-abc.ap-southeast-2.rds.amazonaws.com",
		RDSPort:         5432,
	}
}

func deps(aws *fakeAWS) Deps {
	return Deps{AWS: aws, Profile: "staging", LookupHost: func(context.Context, string) ([]string, error) {
		return nil, errors.New("no lookups expected")
	}}
}

func resultFor(r Report, check string) (Result, bool) {
	for _, res := range r.Results {
		if res.Check == check {
			return res, true
		}
	}
	return Result{}, false
}
