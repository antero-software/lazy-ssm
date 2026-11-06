package diagnose

import (
	"bytes"
	"testing"
)

func TestHeadline(t *testing.T) {
	pass := Result{Check: "AWS login", Status: Pass}
	warn := Result{Check: "Instance", Status: Warn, Message: "w"}
	fail := Result{Check: "SSM", Status: Fail, Message: "f"}

	if got, ok := (Report{Results: []Result{pass, warn, fail}}).Headline(); !ok || got != fail {
		t.Errorf("Headline = (%+v, %v), want the Fail", got, ok)
	}
	if got, ok := (Report{Results: []Result{pass, warn}}).Headline(); !ok || got != warn {
		t.Errorf("Headline = (%+v, %v), want the Warn", got, ok)
	}
	if _, ok := (Report{Results: []Result{pass}}).Headline(); ok {
		t.Error("Headline ok = true for an all-pass report")
	}
}

func TestHasFail(t *testing.T) {
	if (Report{Results: []Result{{Status: Pass}, {Status: Warn}, {Status: CouldNotCheck}}}).HasFail() {
		t.Error("HasFail = true with no Fail")
	}
	if !(Report{Results: []Result{{Status: Pass}, {Status: Fail}}}).HasFail() {
		t.Error("HasFail = false with a Fail")
	}
}

func TestWrite_Format(t *testing.T) {
	r := Report{
		Tunnel:      "staging-db",
		Description: "Staging DB",
		Results: []Result{
			{Check: "AWS login", Status: Pass},
			{Check: "Same VPC", Status: Fail,
				Message: "Instance 'bastion-prod-1' (vpc-111) is not in the same VPC as the database (vpc-222).",
				Fix:     "Check instance_pattern in config.yaml."},
		},
	}
	var buf bytes.Buffer
	r.Write(&buf)

	want := "staging-db (Staging DB)\n" +
		"  PASS            AWS login\n" +
		"  FAIL            Same VPC: Instance 'bastion-prod-1' (vpc-111) is not in the same VPC as the database (vpc-222).\n" +
		"                  Fix: Check instance_pattern in config.yaml.\n"
	if got := buf.String(); got != want {
		t.Errorf("Write =\n%s\nwant\n%s", got, want)
	}
}

func TestLines(t *testing.T) {
	r := Report{Results: []Result{
		{Check: "AWS login", Status: Pass},
		{Check: "SSM", Status: Fail, Message: "m", Fix: "f"},
	}}
	got := r.Lines()
	want := []string{"diagnose: AWS login: PASS", "diagnose: SSM: FAIL: m Fix: f"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Lines = %q, want %q", got, want)
	}
}

func TestStatusString(t *testing.T) {
	for s, want := range map[Status]string{Pass: "PASS", Warn: "WARN", Fail: "FAIL", CouldNotCheck: "COULD NOT CHECK"} {
		if s.String() != want {
			t.Errorf("%d.String() = %q, want %q", s, s.String(), want)
		}
	}
}
