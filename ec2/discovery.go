package ec2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/antero-software/lazy-ssm/awscli"
)

// instanceQuery flattens describe-instances output to the fields lazy-ssm uses.
const instanceQuery = "Reservations[].Instances[].{ID:InstanceId,Name:Tags[?Key=='Name']|[0].Value,VPC:VpcId,IP:PrivateIpAddress,State:State.Name,SGs:SecurityGroups[].GroupId,Profile:IamInstanceProfile.Arn}"

// ErrInstanceNotFound means no instance has the requested ID.
var ErrInstanceNotFound = errors.New("instance not found")

// Instance is one EC2 instance, with the details tunnels and diagnosis need.
type Instance struct {
	ID, Name, VPCID, PrivateIP, State string
	SecurityGroupIDs                  []string
	HasIAMProfile                     bool
}

// InstanceDiscovery handles EC2 instance discovery
type InstanceDiscovery struct {
	aws awscli.Runner
}

// NewInstanceDiscovery creates a new instance discovery helper
func NewInstanceDiscovery(cliPath, profile, region string) *InstanceDiscovery {
	return NewInstanceDiscoveryWithRunner(awscli.CLI{Path: cliPath, Profile: profile, Region: region})
}

// NewInstanceDiscoveryWithRunner creates a discovery helper that uses r.
func NewInstanceDiscoveryWithRunner(r awscli.Runner) *InstanceDiscovery {
	return &InstanceDiscovery{aws: r}
}

// ListCandidates returns the running instances whose Name tag matches
// pattern (wildcards allowed), in the order AWS returns them.
func (d *InstanceDiscovery) ListCandidates(ctx context.Context, pattern string) ([]Instance, error) {
	out, err := d.aws.Run(ctx, "ec2", "describe-instances",
		"--filters",
		fmt.Sprintf("Name=tag:Name,Values=%s", pattern),
		"Name=instance-state-name,Values=running",
		"--query", instanceQuery,
		"--output", "json",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query EC2 instances: %w", err)
	}
	return parseInstances(out)
}

// DescribeInstance returns one instance by ID, in any state.
func (d *InstanceDiscovery) DescribeInstance(ctx context.Context, id string) (Instance, error) {
	out, err := d.aws.Run(ctx, "ec2", "describe-instances",
		"--instance-ids", id,
		"--query", instanceQuery,
		"--output", "json",
	)
	var cliErr *awscli.Error
	if errors.As(err, &cliErr) && strings.Contains(cliErr.Stderr, "InvalidInstanceID") {
		return Instance{}, fmt.Errorf("instance %s: %w", id, ErrInstanceNotFound)
	}
	if err != nil {
		return Instance{}, fmt.Errorf("failed to describe instance %s: %w", id, err)
	}
	instances, err := parseInstances(out)
	if err != nil {
		return Instance{}, err
	}
	if len(instances) == 0 {
		return Instance{}, fmt.Errorf("instance %s: %w", id, ErrInstanceNotFound)
	}
	return instances[0], nil
}

// PickSSMCandidate returns the instance a tunnel uses: the first one with an
// IAM instance profile. eligible is how many candidates have one.
func PickSSMCandidate(c []Instance) (chosen Instance, eligible int, ok bool) {
	for _, inst := range c {
		if !inst.HasIAMProfile {
			continue
		}
		if eligible == 0 {
			chosen = inst
		}
		eligible++
	}
	return chosen, eligible, eligible > 0
}

// FormatEligible lists the candidates that have an IAM instance profile as
// "name (id, vpc), ...".
func FormatEligible(candidates []Instance) string {
	var parts []string
	for _, c := range candidates {
		if c.HasIAMProfile {
			parts = append(parts, fmt.Sprintf("%s (%s, %s)", c.Name, c.ID, c.VPCID))
		}
	}
	return strings.Join(parts, ", ")
}

// SpansVPCs reports whether the candidates SSM can use are in more than one
// VPC. Several matches in one VPC (an auto-scaling group) are normal.
func SpansVPCs(candidates []Instance) bool {
	first := ""
	for _, c := range candidates {
		if !c.HasIAMProfile {
			continue
		}
		if first == "" {
			first = c.VPCID
		} else if c.VPCID != first {
			return true
		}
	}
	return false
}

type rawInstance struct {
	ID      string   `json:"ID"`
	Name    string   `json:"Name"`
	VPC     string   `json:"VPC"`
	IP      string   `json:"IP"`
	State   string   `json:"State"`
	SGs     []string `json:"SGs"`
	Profile *string  `json:"Profile"`
}

func parseInstances(b []byte) ([]Instance, error) {
	var raw []rawInstance
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("unexpected describe-instances output: %w", err)
	}
	out := make([]Instance, 0, len(raw))
	for _, r := range raw {
		out = append(out, Instance{
			ID:               r.ID,
			Name:             r.Name,
			VPCID:            r.VPC,
			PrivateIP:        r.IP,
			State:            r.State,
			SecurityGroupIDs: r.SGs,
			HasIAMProfile:    r.Profile != nil,
		})
	}
	return out, nil
}
