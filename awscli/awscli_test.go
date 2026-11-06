package awscli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	got := buildArgs("p", "r", []string{"sts", "get-caller-identity"})
	want := []string{"--region", "r", "--profile", "p", "sts", "get-caller-identity"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildArgs = %q, want %q", got, want)
	}

	if got := buildArgs("", "", []string{"x"}); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("buildArgs with no profile or region = %q, want [x]", got)
	}
}

func TestIsAccessDenied(t *testing.T) {
	denied := &Error{Args: []string{"ec2", "describe-vpcs"}, Stderr: "An error occurred (UnauthorizedOperation) when calling the DescribeVpcs operation", Err: errors.New("exit status 254")}
	if !IsAccessDenied(denied) {
		t.Error("UnauthorizedOperation should count as access denied")
	}
	if IsAccessDenied(errors.New("plain")) {
		t.Error("a plain error is not access denied")
	}
}

func TestError_DoesNotIncludeStderr(t *testing.T) {
	err := &Error{
		Args:   []string{"ssm", "describe-instance-information", "--output", "json"},
		Stderr: "User: arn:aws:sts::1:assumed-role/X/keith@example.com is not authorized",
		Err:    errors.New("exit status 254"),
	}
	msg := err.Error()
	if strings.Contains(msg, "arn:") || strings.Contains(msg, "@") {
		t.Errorf("Error() leaks stderr: %q", msg)
	}
	if msg != "aws ssm describe-instance-information: exit status 254" {
		t.Errorf("Error() = %q", msg)
	}
}
