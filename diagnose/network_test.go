package diagnose

import (
	"context"
	"errors"
	"testing"
)

const (
	dbInstances     = `[{"ID":"stg-1","Addr":"stg-1.abc.ap-southeast-2.rds.amazonaws.com","Status":"available","VPC":"vpc-222","SGs":["sg-123"],"Cluster":"stg"}]`
	dbClusters      = `[{"ID":"stg","Writer":"stg.cluster-abc.ap-southeast-2.rds.amazonaws.com","Reader":"stg.cluster-ro-abc.ap-southeast-2.rds.amazonaws.com","Status":"available","SGs":["sg-123"]}]`
	customEndpoints = `[{"Cluster":"stg","Addr":"stg-analytics.cluster-custom-abc.ap-southeast-2.rds.amazonaws.com"}]`

	prodBastion = `[{"ID":"i-0prod","Name":"bastion-prod-1","VPC":"vpc-111","IP":"10.0.1.5","State":"running","SGs":["sg-456"],"Profile":"p"}]`
	stgBastion  = `[{"ID":"i-0stg","Name":"bastion-stg-1","VPC":"vpc-222","IP":"10.2.1.5","State":"running","SGs":["sg-456"],"Profile":"p"}]`

	rulesAllowBastion = `[{"IpProtocol":"tcp","FromPort":5432,"ToPort":5432,"UserIdGroupPairs":[{"GroupId":"sg-456"}],"IpRanges":[]}]`
	rulesBlock        = `[{"IpProtocol":"tcp","FromPort":5432,"ToPort":5432,"UserIdGroupPairs":[{"GroupId":"sg-999"}],"IpRanges":[]}]`

	vpc111CIDRs = `["10.0.0.0/16"]`
)

// networkAWS answers every call a full diagnosis makes.
func networkAWS(bastion, rules string) *fakeAWS {
	return newFakeAWS().
		on("sts get-caller-identity", callerIdentity).
		on("ec2 describe-instances", bastion).
		on("ssm describe-instance-information", ssmOnline).
		on("rds describe-db-instances", dbInstances).
		on("rds describe-db-clusters", dbClusters).
		on("rds describe-db-cluster-endpoints", customEndpoints).
		on("ec2 describe-security-groups", rules).
		on("ec2 describe-vpcs", vpc111CIDRs)
}

func lookup(ips ...string) func(context.Context, string) ([]string, error) {
	return func(context.Context, string) ([]string, error) {
		if len(ips) == 0 {
			return nil, errors.New("no such host")
		}
		return ips, nil
	}
}

func TestFindDatabase_ClusterWriterCaseAndTrailingDot(t *testing.T) {
	aws := newFakeAWS().on("rds describe-db-instances", dbInstances).on("rds describe-db-clusters", dbClusters)

	db, found, err := findDatabase(context.Background(), aws, "STG.cluster-abc.ap-southeast-2.rds.amazonaws.com.")

	if err != nil || !found {
		t.Fatalf("findDatabase = (%+v, %v, %v), want found", db, found, err)
	}
	if db.ID != "stg" || db.VPCID != "vpc-222" || db.Status != "available" || len(db.SecurityGroupIDs) != 1 || db.SecurityGroupIDs[0] != "sg-123" {
		t.Errorf("db = %+v", db)
	}
}

func TestFindDatabase_ReaderAndCustomEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"stg.cluster-ro-abc.ap-southeast-2.rds.amazonaws.com",
		"stg-analytics.cluster-custom-abc.ap-southeast-2.rds.amazonaws.com",
	} {
		aws := newFakeAWS().on("rds describe-db-instances", dbInstances).on("rds describe-db-clusters", dbClusters).
			on("rds describe-db-cluster-endpoints", customEndpoints)

		db, found, err := findDatabase(context.Background(), aws, endpoint)

		if err != nil || !found || db.ID != "stg" || db.VPCID != "vpc-222" {
			t.Errorf("%s: findDatabase = (%+v, %v, %v), want cluster stg in vpc-222", endpoint, db, found, err)
		}
	}
}

func TestFindDatabase_InstanceEndpoint(t *testing.T) {
	aws := newFakeAWS().on("rds describe-db-instances", dbInstances)

	db, found, err := findDatabase(context.Background(), aws, "stg-1.abc.ap-southeast-2.rds.amazonaws.com")

	if err != nil || !found || db.ID != "stg-1" || db.VPCID != "vpc-222" {
		t.Errorf("findDatabase = (%+v, %v, %v), want instance stg-1", db, found, err)
	}
}

func TestFindDatabase_NotFound(t *testing.T) {
	aws := newFakeAWS().on("rds describe-db-instances", dbInstances).on("rds describe-db-clusters", dbClusters).
		on("rds describe-db-cluster-endpoints", customEndpoints)

	_, found, err := findDatabase(context.Background(), aws, "redis.abc.apse2.cache.amazonaws.com")

	if err != nil || found {
		t.Errorf("findDatabase = (found %v, err %v), want not found and no error", found, err)
	}
}

func TestAllowsFrom(t *testing.T) {
	srcGroups := []string{"sg-456"}
	srcIP := "10.0.1.5"
	tests := []struct {
		name  string
		rules []sgRule
		want  bool
	}{
		{"source security group", []sgRule{{Protocol: "tcp", FromPort: 5432, ToPort: 5432, SourceGroupIDs: []string{"sg-456"}}}, true},
		{"CIDR contains instance IP", []sgRule{{Protocol: "tcp", FromPort: 5432, ToPort: 5432, CIDRs: []string{"10.0.0.0/16"}}}, true},
		{"port inside range", []sgRule{{Protocol: "tcp", FromPort: 5000, ToPort: 6000, CIDRs: []string{"10.0.0.0/16"}}}, true},
		{"all traffic", []sgRule{{Protocol: "-1", SourceGroupIDs: []string{"sg-456"}}}, true},
		{"wrong port", []sgRule{{Protocol: "tcp", FromPort: 3306, ToPort: 3306, SourceGroupIDs: []string{"sg-456"}}}, false},
		{"udp only", []sgRule{{Protocol: "udp", FromPort: 5432, ToPort: 5432, SourceGroupIDs: []string{"sg-456"}}}, false},
		{"other source", []sgRule{{Protocol: "tcp", FromPort: 5432, ToPort: 5432, SourceGroupIDs: []string{"sg-999"}, CIDRs: []string{"192.168.0.0/16"}}}, false},
		{"no rules", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allowsFrom(tt.rules, 5432, srcGroups, srcIP); got != tt.want {
				t.Errorf("allowsFrom = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRun_VPCMismatchFirewallBlocks_FailsWithVPCMessage(t *testing.T) {
	r := Run(context.Background(), stagingTunnel(), deps(networkAWS(prodBastion, rulesBlock)))

	head, ok := r.Headline()
	if !ok || head.Check != "Same VPC" || head.Status != Fail {
		t.Fatalf("Headline = (%+v, %v), want Same VPC Fail", head, ok)
	}
	if head.Message != "Instance 'bastion-prod-1' (vpc-111) is not in the same VPC as the database (vpc-222)." {
		t.Errorf("message = %q", head.Message)
	}
	if head.Fix != "Check instance_pattern in config.yaml." {
		t.Errorf("fix = %q", head.Fix)
	}
}

func TestRun_VPCMismatchUsesInstanceIDFix(t *testing.T) {
	tc := stagingTunnel()
	tc.InstancePattern = ""
	tc.InstanceID = "i-0prod"

	r := Run(context.Background(), tc, deps(networkAWS(prodBastion, rulesBlock)))

	if head, _ := r.Headline(); head.Fix != "Check instance_id in config.yaml." {
		t.Errorf("fix = %q", head.Fix)
	}
}

func TestRun_VPCMismatchFirewallAllows_Warns(t *testing.T) {
	r := Run(context.Background(), stagingTunnel(), deps(networkAWS(prodBastion, rulesAllowBastion)))

	vpc, _ := resultFor(r, "Same VPC")
	if vpc.Status != Warn || vpc.Message != "The instance and database are in different VPCs. This works only if the VPCs are connected." {
		t.Errorf("Same VPC = %+v", vpc)
	}
	if r.HasFail() {
		t.Errorf("HasFail = true, want no failures: %+v", r.Results)
	}
}

func TestRun_VPCMismatchFirewallDenied_WarnsWithVPCMessage(t *testing.T) {
	aws := networkAWS(prodBastion, rulesBlock).fail("ec2 describe-security-groups", accessDenied("ec2", "describe-security-groups"))

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	vpc, _ := resultFor(r, "Same VPC")
	if vpc.Status != Warn || vpc.Message != "Instance 'bastion-prod-1' (vpc-111) is not in the same VPC as the database (vpc-222)." {
		t.Errorf("Same VPC = %+v", vpc)
	}
	if fw, _ := resultFor(r, "Firewall"); fw.Status != CouldNotCheck || fw.Message != "Your AWS role is not allowed to run ec2 describe-security-groups." {
		t.Errorf("Firewall = %+v", fw)
	}
}

func TestRun_DatabaseStopped_Fails(t *testing.T) {
	aws := networkAWS(stgBastion, rulesAllowBastion).on("rds describe-db-clusters",
		`[{"ID":"stg","Writer":"stg.cluster-abc.ap-southeast-2.rds.amazonaws.com","Reader":"","Status":"stopped","SGs":["sg-123"]}]`)

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	head, ok := r.Headline()
	if !ok || head.Check != "Database running" || head.Message != "Database 'stg' is not running (status: stopped)." {
		t.Errorf("Headline = (%+v, %v)", head, ok)
	}
}

func TestRun_SameVPCFirewallBlocks_FailsOnFirewall(t *testing.T) {
	r := Run(context.Background(), stagingTunnel(), deps(networkAWS(stgBastion, rulesBlock)))

	if vpc, _ := resultFor(r, "Same VPC"); vpc.Status != Pass {
		t.Errorf("Same VPC = %+v, want Pass", vpc)
	}
	head, ok := r.Headline()
	want := "Database security group sg-123 does not allow port 5432 from instance 'bastion-stg-1' (sg-456, 10.2.1.5)."
	if !ok || head.Check != "Firewall" || head.Status != Fail || head.Message != want {
		t.Errorf("Headline = (%+v, %v), want Firewall %q", head, ok, want)
	}
	if head.Fix != "Check instance_pattern in config.yaml, or ask for an inbound rule on the database security group." {
		t.Errorf("fix = %q", head.Fix)
	}
}

func TestRun_NonRDSIPOutsideVPC_Warns(t *testing.T) {
	tc := stagingTunnel()
	tc.RDSEndpoint = "10.40.210.28"

	r := Run(context.Background(), tc, deps(networkAWS(prodBastion, rulesAllowBastion)))

	db, _ := resultFor(r, "Database")
	if db.Status != CouldNotCheck || db.Message != "10.40.210.28 is not an RDS database in this account and region, so lazy-ssm used the address check instead." {
		t.Errorf("Database = %+v", db)
	}
	addr, _ := resultFor(r, "Address in instance VPC")
	if addr.Status != Warn || addr.Message != "Database address 10.40.210.28 is outside the instance VPC (vpc-111, 10.0.0.0/16)." {
		t.Errorf("Address = %+v", addr)
	}
	if addr.Fix != "Check instance_pattern in config.yaml." {
		t.Errorf("fix = %q", addr.Fix)
	}
	if _, ok := resultFor(r, "Same VPC"); ok {
		t.Error("Same VPC result present for a non-RDS target")
	}
}

func TestRun_NonRDSHostnameInsideVPC_Passes(t *testing.T) {
	tc := stagingTunnel()
	tc.RDSEndpoint = "prod-redis-grp.wbijvy.apse2.cache.amazonaws.com"
	d := deps(networkAWS(prodBastion, rulesAllowBastion))
	d.LookupHost = lookup("10.0.5.5")

	r := Run(context.Background(), tc, d)

	if addr, _ := resultFor(r, "Address in instance VPC"); addr.Status != Pass {
		t.Errorf("Address = %+v, want Pass", addr)
	}
	if fw, _ := resultFor(r, "Firewall"); fw.Status != CouldNotCheck {
		t.Errorf("Firewall = %+v, want CouldNotCheck (skipped for non-RDS)", fw)
	}
	if _, ok := r.Headline(); ok {
		t.Errorf("Headline found for a healthy non-RDS tunnel: %+v", r.Results)
	}
}

func TestRun_NonRDSUnresolvable_CouldNotCheck(t *testing.T) {
	tc := stagingTunnel()
	tc.RDSEndpoint = "internal.db.example.local"
	d := deps(networkAWS(prodBastion, rulesAllowBastion))
	d.LookupHost = lookup()

	r := Run(context.Background(), tc, d)

	addr, _ := resultFor(r, "Address in instance VPC")
	if addr.Status != CouldNotCheck || addr.Message != "Could not resolve internal.db.example.local on this Mac." {
		t.Errorf("Address = %+v", addr)
	}
}

func TestRun_AllPass_NoHeadline(t *testing.T) {
	r := Run(context.Background(), stagingTunnel(), deps(networkAWS(stgBastion, rulesAllowBastion)))

	if head, ok := r.Headline(); ok {
		t.Errorf("Headline = %+v, want none; results %+v", head, r.Results)
	}
	if _, ok := resultFor(r, "Address in instance VPC"); ok {
		t.Error("Address check ran for an RDS database")
	}
	for _, name := range []string{"AWS login", "Instance", "SSM", "Database", "Database running", "Same VPC", "Firewall"} {
		if res, ok := resultFor(r, name); !ok || res.Status != Pass {
			t.Errorf("%s = (%+v, %v), want Pass", name, res, ok)
		}
	}
}

func TestRun_InstanceMissingStillChecksDatabase(t *testing.T) {
	aws := networkAWS(`[]`, rulesAllowBastion)

	r := Run(context.Background(), stagingTunnel(), deps(aws))

	if db, _ := resultFor(r, "Database"); db.Status != Pass {
		t.Errorf("Database = %+v, want Pass", db)
	}
	for _, name := range []string{"Same VPC", "Firewall"} {
		if res, _ := resultFor(r, name); res.Status != CouldNotCheck || res.Message != "Skipped because an earlier check failed." {
			t.Errorf("%s = %+v, want skipped", name, res)
		}
	}
}

func TestRun_RDSAddressNotInAccount_WarnsProfileAndRegion(t *testing.T) {
	aws := networkAWS(stgBastion, rulesAllowBastion)
	tc := stagingTunnel()
	tc.RDSEndpoint = "other.cluster-xyz.ap-southeast-2.rds.amazonaws.com"
	d := deps(aws)
	d.LookupHost = lookup()

	r := Run(context.Background(), tc, d)

	db, _ := resultFor(r, "Database")
	if db.Status != Warn || db.Message != "other.cluster-xyz.ap-southeast-2.rds.amazonaws.com is an RDS address, but lazy-ssm cannot find it in this account and region." {
		t.Errorf("Database = %+v", db)
	}
	if db.Fix != "Check aws_profile and aws_region in config.yaml." {
		t.Errorf("fix = %q", db.Fix)
	}
}

func TestRun_InstanceAndRDSDatabaseBothMissing_PointsAtProfile(t *testing.T) {
	aws := networkAWS(`[]`, rulesAllowBastion)
	tc := stagingTunnel()
	tc.RDSEndpoint = "other.cluster-xyz.ap-southeast-2.rds.amazonaws.com"

	r := Run(context.Background(), tc, deps(aws))

	head, ok := r.Headline()
	if !ok || head.Check != "Instance" || head.Status != Fail {
		t.Fatalf("Headline = (%+v, %v), want the Instance failure", head, ok)
	}
	want := "Check aws_profile and aws_region in config.yaml. Neither the instance nor the database is in this account and region."
	if head.Fix != want {
		t.Errorf("fix = %q, want %q", head.Fix, want)
	}
}
