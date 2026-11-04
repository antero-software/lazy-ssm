package ec2

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/antero-software/lazy-ssm/awscli"
)

const twoInstances = `[
  {"ID": "i-aaa", "Name": "bastion-stg-1", "VPC": "vpc-111", "IP": "10.0.1.5", "State": "running",
   "SGs": ["sg-1", "sg-2"], "Profile": "arn:aws:iam::1:instance-profile/ssm"},
  {"ID": "i-bbb", "Name": "bastion-stg-2", "VPC": "vpc-111", "IP": "10.0.1.6", "State": "running",
   "SGs": ["sg-1"], "Profile": null}
]`

func TestParseInstances(t *testing.T) {
	got, err := parseInstances([]byte(twoInstances))
	if err != nil {
		t.Fatalf("parseInstances: %v", err)
	}
	want := []Instance{
		{ID: "i-aaa", Name: "bastion-stg-1", VPCID: "vpc-111", PrivateIP: "10.0.1.5", State: "running", SecurityGroupIDs: []string{"sg-1", "sg-2"}, HasIAMProfile: true},
		{ID: "i-bbb", Name: "bastion-stg-2", VPCID: "vpc-111", PrivateIP: "10.0.1.6", State: "running", SecurityGroupIDs: []string{"sg-1"}, HasIAMProfile: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseInstances =\n%+v\nwant\n%+v", got, want)
	}
}

func TestPickSSMCandidate(t *testing.T) {
	noProfile := Instance{ID: "i-0", Name: "no-profile"}
	a := Instance{ID: "i-a", Name: "a", HasIAMProfile: true}
	b := Instance{ID: "i-b", Name: "b", HasIAMProfile: true}

	chosen, eligible, ok := PickSSMCandidate([]Instance{noProfile, a, b})
	if !ok || chosen.ID != "i-a" || eligible != 2 {
		t.Errorf("PickSSMCandidate = (%v, %d, %v), want (i-a, 2, true)", chosen.ID, eligible, ok)
	}

	if _, eligible, ok := PickSSMCandidate([]Instance{noProfile}); ok || eligible != 0 {
		t.Errorf("PickSSMCandidate(no profile) = (%d, %v), want (0, false)", eligible, ok)
	}

	if _, _, ok := PickSSMCandidate(nil); ok {
		t.Error("PickSSMCandidate(nil) ok = true, want false")
	}
}

type recordingRunner struct {
	args []string
	out  string
}

func (r *recordingRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.args = args
	return []byte(r.out), nil
}

func TestListCandidates_QueriesRunningInstancesByName(t *testing.T) {
	r := &recordingRunner{out: twoInstances}
	d := NewInstanceDiscoveryWithRunner(r)

	got, err := d.ListCandidates(context.Background(), "bastion-stg-*")
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d instances, want 2", len(got))
	}
	joined := strings.Join(r.args, " ")
	for _, want := range []string{"ec2 describe-instances", "Name=tag:Name,Values=bastion-stg-*", "Name=instance-state-name,Values=running", "--output json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

type fixedRunner struct {
	out string
	err error
}

func (r fixedRunner) Run(context.Context, ...string) ([]byte, error) { return []byte(r.out), r.err }

func TestDescribeInstance_NotFound(t *testing.T) {
	empty := NewInstanceDiscoveryWithRunner(fixedRunner{out: `[]`})
	if _, err := empty.DescribeInstance(context.Background(), "i-0abc"); !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("empty result err = %v, want ErrInstanceNotFound", err)
	}

	invalid := NewInstanceDiscoveryWithRunner(fixedRunner{err: &awscli.Error{
		Args:   []string{"ec2", "describe-instances"},
		Stderr: "An error occurred (InvalidInstanceID.NotFound) when calling the DescribeInstances operation",
		Err:    errors.New("exit status 254"),
	}})
	if _, err := invalid.DescribeInstance(context.Background(), "i-0abc"); !errors.Is(err, ErrInstanceNotFound) {
		t.Errorf("InvalidInstanceID err = %v, want ErrInstanceNotFound", err)
	}
}

func TestFormatEligible(t *testing.T) {
	got := FormatEligible([]Instance{
		{ID: "i-1", Name: "bastion-prod-1", VPCID: "vpc-111", HasIAMProfile: true},
		{ID: "i-2", Name: "no-profile", VPCID: "vpc-111"},
		{ID: "i-3", Name: "bastion-stg-1", VPCID: "vpc-222", HasIAMProfile: true},
	})
	want := "bastion-prod-1 (i-1, vpc-111), bastion-stg-1 (i-3, vpc-222)"
	if got != want {
		t.Errorf("FormatEligible = %q, want %q", got, want)
	}
}
