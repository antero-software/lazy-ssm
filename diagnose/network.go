package diagnose

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/antero-software/lazy-ssm/awscli"
	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/ec2"
)

// Check names, in result order (after the ones in checks.go).
const (
	checkDatabase        = "Database"
	checkDatabaseRunning = "Database running"
	checkSameVPC         = "Same VPC"
	checkAddress         = "Address in instance VPC"
	checkFirewall        = "Firewall"
)

// dbTarget is the RDS instance or Aurora cluster behind rds_endpoint.
type dbTarget struct {
	ID, Status, VPCID string
	SecurityGroupIDs  []string
}

type rdsInstance struct {
	ID      string   `json:"ID"`
	Addr    string   `json:"Addr"`
	Status  string   `json:"Status"`
	VPC     string   `json:"VPC"`
	SGs     []string `json:"SGs"`
	Cluster *string  `json:"Cluster"`
}

type rdsCluster struct {
	ID     string   `json:"ID"`
	Writer string   `json:"Writer"`
	Reader string   `json:"Reader"`
	Status string   `json:"Status"`
	SGs    []string `json:"SGs"`
}

type rdsClusterEndpoint struct {
	Cluster string `json:"Cluster"`
	Addr    string `json:"Addr"`
}

func sameHost(a, b string) bool {
	return b != "" && strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

func runJSON(ctx context.Context, aws awscli.Runner, v any, args ...string) error {
	out, err := aws.Run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("unexpected output from aws %s %s: %w", args[0], args[1], err)
	}
	return nil
}

// findDatabase looks up the RDS instance or Aurora cluster whose endpoint is
// endpoint. found is false when it is not an RDS endpoint in this account
// and region.
func findDatabase(ctx context.Context, aws awscli.Runner, endpoint string) (dbTarget, bool, error) {
	// The instance and cluster lookups are separate permissions. If the
	// instance lookup is denied, still try the clusters (Aurora endpoints);
	// only the cluster's VPC (which comes from its instances) is then unknown.
	var instances []rdsInstance
	instErr := runJSON(ctx, aws, &instances, "rds", "describe-db-instances", "--output", "json",
		"--query", "DBInstances[].{ID:DBInstanceIdentifier,Addr:Endpoint.Address,Status:DBInstanceStatus,VPC:DBSubnetGroup.VpcId,SGs:VpcSecurityGroups[].VpcSecurityGroupId,Cluster:DBClusterIdentifier}")
	firstErr := func(err error) error {
		if instErr != nil {
			return instErr
		}
		return err
	}
	for _, in := range instances {
		if sameHost(endpoint, in.Addr) {
			return dbTarget{ID: in.ID, Status: in.Status, VPCID: in.VPC, SecurityGroupIDs: in.SGs}, true, nil
		}
	}

	var clusters []rdsCluster
	if err := runJSON(ctx, aws, &clusters, "rds", "describe-db-clusters", "--output", "json",
		"--query", "DBClusters[].{ID:DBClusterIdentifier,Writer:Endpoint,Reader:ReaderEndpoint,Status:Status,SGs:VpcSecurityGroups[].VpcSecurityGroupId}"); err != nil {
		return dbTarget{}, false, firstErr(err)
	}
	clusterTarget := func(c rdsCluster) dbTarget {
		t := dbTarget{ID: c.ID, Status: c.Status, SecurityGroupIDs: c.SGs}
		for _, in := range instances {
			if in.Cluster != nil && *in.Cluster == c.ID {
				t.VPCID = in.VPC
				break
			}
		}
		return t
	}
	for _, c := range clusters {
		if sameHost(endpoint, c.Writer) || sameHost(endpoint, c.Reader) {
			return clusterTarget(c), true, nil
		}
	}

	var custom []rdsClusterEndpoint
	if err := runJSON(ctx, aws, &custom, "rds", "describe-db-cluster-endpoints", "--output", "json",
		"--query", "DBClusterEndpoints[].{Cluster:DBClusterIdentifier,Addr:Endpoint}"); err != nil {
		return dbTarget{}, false, firstErr(err)
	}
	for _, e := range custom {
		if !sameHost(endpoint, e.Addr) {
			continue
		}
		for _, c := range clusters {
			if c.ID == e.Cluster {
				return clusterTarget(c), true, nil
			}
		}
	}
	// Not a cluster. If the instance lookup was denied, it may still be an
	// RDS instance we could not see.
	return dbTarget{}, false, instErr
}

// sgRule is one inbound security group rule.
type sgRule struct {
	Protocol         string // "tcp", "udp", "-1"
	FromPort, ToPort int
	SourceGroupIDs   []string
	CIDRs            []string
	PrefixLists      bool // the rule has managed prefix list sources
	IPv6             bool // the rule has IPv6 CIDR sources
}

// allowsFrom reports whether any rule lets TCP traffic on port in from one of
// srcGroups or from srcIP.
func allowsFrom(rules []sgRule, port int, srcGroups []string, srcIP string) bool {
	for _, r := range rules {
		if !r.coversPort(port) {
			continue
		}
		for _, g := range r.SourceGroupIDs {
			for _, s := range srcGroups {
				if g == s {
					return true
				}
			}
		}
		if srcIP != "" && ipInCIDRs(srcIP, r.CIDRs) {
			return true
		}
	}
	return false
}

// coversPort reports whether the rule applies to TCP traffic on port.
func (r sgRule) coversPort(port int) bool {
	return r.Protocol == "-1" || (r.Protocol == "tcp" && r.FromPort <= port && port <= r.ToPort)
}

// ipInCIDRs reports whether ip is inside any of cidrs.
func ipInCIDRs(ip string, cidrs []string) bool {
	addr := net.ParseIP(ip)
	if addr == nil {
		return false
	}
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(addr) {
			return true
		}
	}
	return false
}

type rawPermission struct {
	IpProtocol       string `json:"IpProtocol"`
	FromPort         *int   `json:"FromPort"`
	ToPort           *int   `json:"ToPort"`
	UserIdGroupPairs []struct {
		GroupId string `json:"GroupId"`
	} `json:"UserIdGroupPairs"`
	IpRanges []struct {
		CidrIp string `json:"CidrIp"`
	} `json:"IpRanges"`
	PrefixListIds []struct {
		PrefixListId string `json:"PrefixListId"`
	} `json:"PrefixListIds"`
	Ipv6Ranges []struct {
		CidrIpv6 string `json:"CidrIpv6"`
	} `json:"Ipv6Ranges"`
}

func (p rawPermission) rule() sgRule {
	r := sgRule{Protocol: p.IpProtocol, ToPort: 65535}
	if p.FromPort != nil {
		r.FromPort = *p.FromPort
	}
	if p.ToPort != nil {
		r.ToPort = *p.ToPort
	}
	for _, g := range p.UserIdGroupPairs {
		r.SourceGroupIDs = append(r.SourceGroupIDs, g.GroupId)
	}
	for _, c := range p.IpRanges {
		r.CIDRs = append(r.CIDRs, c.CidrIp)
	}
	r.PrefixLists = len(p.PrefixListIds) > 0
	r.IPv6 = len(p.Ipv6Ranges) > 0
	return r
}

const profileRegionFix = "Check aws_profile and aws_region in config.yaml."

// isRDSHostname reports whether host is an RDS instance or cluster endpoint
// name. RDS Proxy endpoints are not looked up, so they do not count.
func isRDSHostname(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(h, ".rds.amazonaws.com") && !strings.Contains(h, ".proxy-")
}

// databaseRunningResult judges the RDS status. Many statuses other than
// "available" (backing-up, modifying, storage-optimization...) still serve
// connections, so only clearly-down states fail.
func databaseRunningResult(db dbTarget) Result {
	down := []string{"stopped", "stopping", "starting", "rebooting", "failed", "deleting", "creating",
		"insufficient-capacity", "restore-error", "storage-full"}
	downPrefixes := []string{"inaccessible-encryption-credentials", "incompatible-"}
	busy := []string{"upgrading", "maintenance", "renaming", "failing-over"}

	isDown := false
	for _, s := range down {
		isDown = isDown || db.Status == s
	}
	for _, p := range downPrefixes {
		isDown = isDown || strings.HasPrefix(db.Status, p)
	}
	if isDown {
		return Result{Check: checkDatabaseRunning, Status: Fail,
			Message: fmt.Sprintf("Database '%s' is not available (status: %s).", db.ID, db.Status)}
	}
	for _, s := range busy {
		if db.Status == s {
			return Result{Check: checkDatabaseRunning, Status: Warn,
				Message: fmt.Sprintf("Database '%s' is busy (status: %s). Connections may fail until it finishes.", db.ID, db.Status)}
		}
	}
	return Result{Check: checkDatabaseRunning, Status: Pass}
}

// configKey is the config setting that chose the instance.
func configKey(tc config.TunnelConfig) string {
	if tc.InstanceID != "" {
		return "instance_id"
	}
	return "instance_pattern"
}

// networkResults runs the database, VPC, address and firewall checks.
// haveInstance is false when the instance checks did not find one.
// rdsMissing is true when rds_endpoint is an RDS address that is not in this
// account and region.
func networkResults(ctx context.Context, tc config.TunnelConfig, d Deps, inst ec2.Instance, haveInstance bool) (results []Result, rdsMissing bool) {
	db, isRDS, err := findDatabase(ctx, d.AWS, tc.RDSEndpoint)
	if err != nil || !isRDS {
		var out []Result
		switch {
		case err != nil:
			out = append(out, awsFailure(checkDatabase, err, "rds", "describe-db-instances"))
		case isRDSHostname(tc.RDSEndpoint):
			rdsMissing = true
			out = append(out, Result{Check: checkDatabase, Status: CouldNotCheck,
				Message: fmt.Sprintf("%s is an RDS address, but lazy-ssm cannot find it in this account and region, so it used the address check instead.", tc.RDSEndpoint)})
		default:
			out = append(out, Result{Check: checkDatabase, Status: CouldNotCheck,
				Message: fmt.Sprintf("%s is not an RDS database in this account and region, so lazy-ssm used the address check instead.", tc.RDSEndpoint)})
		}
		if haveInstance {
			out = append(out, addressResult(ctx, tc, d, inst))
		} else {
			out = append(out, skipped(checkAddress)...)
		}
		return append(out, Result{Check: checkFirewall, Status: CouldNotCheck,
			Message: "Skipped because the database is not an RDS database lazy-ssm could find."}), rdsMissing
	}

	out := []Result{{Check: checkDatabase, Status: Pass, Message: fmt.Sprintf("Found '%s' (%s).", db.ID, db.VPCID)}}
	out = append(out, databaseRunningResult(db))
	if !haveInstance {
		return append(out, skipped(checkSameVPC, checkFirewall)...), false
	}

	fw := firewallResult(ctx, tc, d.AWS, inst, db)
	return append(out, sameVPCResult(tc, inst, db, fw), fw), false
}

func firewallResult(ctx context.Context, tc config.TunnelConfig, aws awscli.Runner, inst ec2.Instance, db dbTarget) Result {
	args := append([]string{"ec2", "describe-security-groups", "--group-ids"}, db.SecurityGroupIDs...)
	args = append(args, "--output", "json", "--query", "SecurityGroups[].IpPermissions[]")
	var perms []rawPermission
	if err := runJSON(ctx, aws, &perms, args...); err != nil {
		return awsFailure(checkFirewall, err, "ec2", "describe-security-groups")
	}
	rules := make([]sgRule, 0, len(perms))
	for _, p := range perms {
		rules = append(rules, p.rule())
	}
	if allowsFrom(rules, tc.RDSPort, inst.SecurityGroupIDs, inst.PrivateIP) {
		return Result{Check: checkFirewall, Status: Pass}
	}
	// Rules lazy-ssm cannot match against the instance: say so, never FAIL.
	for _, r := range rules {
		if !r.coversPort(tc.RDSPort) {
			continue
		}
		switch {
		case r.PrefixLists:
			return Result{Check: checkFirewall, Status: CouldNotCheck,
				Message: fmt.Sprintf("A database security group rule for port %d uses a managed prefix list, which lazy-ssm cannot check.", tc.RDSPort)}
		case r.IPv6:
			return Result{Check: checkFirewall, Status: CouldNotCheck,
				Message: fmt.Sprintf("A database security group rule for port %d uses IPv6 ranges, which lazy-ssm cannot check.", tc.RDSPort)}
		}
	}
	return Result{
		Check:  checkFirewall,
		Status: Fail,
		Message: fmt.Sprintf("Database security group %s does not allow port %d from instance '%s' (%s, %s).",
			strings.Join(db.SecurityGroupIDs, ", "), tc.RDSPort, inst.Name, strings.Join(inst.SecurityGroupIDs, ", "), inst.PrivateIP),
		Fix: fmt.Sprintf("Check %s in config.yaml, or ask for an inbound rule on the database security group.", configKey(tc)),
	}
}

// sameVPCResult compares VPCs. Different VPCs can be fine when they are
// connected (peering, Transit Gateway), so the firewall result decides
// between Fail and Warn.
func sameVPCResult(tc config.TunnelConfig, inst ec2.Instance, db dbTarget, fw Result) Result {
	if db.VPCID == "" {
		return Result{Check: checkSameVPC, Status: CouldNotCheck, Message: "Could not find which VPC the database is in."}
	}
	if inst.VPCID == db.VPCID {
		return Result{Check: checkSameVPC, Status: Pass, Message: fmt.Sprintf("Both in %s.", db.VPCID)}
	}
	if fw.Status == Pass {
		return Result{Check: checkSameVPC, Status: Warn,
			Message: "The instance and database are in different VPCs. This works only if the VPCs are connected."}
	}
	status := Fail
	if fw.Status == CouldNotCheck {
		status = Warn
	}
	return Result{
		Check:   checkSameVPC,
		Status:  status,
		Message: fmt.Sprintf("Instance '%s' (%s) is not in the same VPC as the database (%s).", inst.Name, inst.VPCID, db.VPCID),
		Fix:     fmt.Sprintf("Check %s in config.yaml.", configKey(tc)),
	}
}

// addressResult checks a non-RDS target: is its IP inside the instance VPC?
func addressResult(ctx context.Context, tc config.TunnelConfig, d Deps, inst ec2.Instance) Result {
	ip := tc.RDSEndpoint
	if net.ParseIP(ip) == nil {
		ip = ""
		lookupHost := d.LookupHost
		if lookupHost == nil {
			lookupHost = net.DefaultResolver.LookupHost
		}
		addrs, err := lookupHost(ctx, tc.RDSEndpoint)
		if err == nil {
			// Prefer IPv4; fall back to IPv6 when that is all there is.
			for _, a := range addrs {
				if p := net.ParseIP(a); p != nil && p.To4() != nil {
					ip = a
					break
				}
			}
			if ip == "" {
				for _, a := range addrs {
					if net.ParseIP(a) != nil {
						ip = a
						break
					}
				}
			}
		}
		if ip == "" {
			return Result{Check: checkAddress, Status: CouldNotCheck,
				Message: fmt.Sprintf("Could not resolve %s on this Mac.", tc.RDSEndpoint)}
		}
	}

	var cidrs []string
	if err := runJSON(ctx, d.AWS, &cidrs, "ec2", "describe-vpcs", "--vpc-ids", inst.VPCID, "--output", "json",
		"--query", "Vpcs[0].[CidrBlockAssociationSet[].CidrBlock, Ipv6CidrBlockAssociationSet[].Ipv6CidrBlock][]"); err != nil {
		return awsFailure(checkAddress, err, "ec2", "describe-vpcs")
	}
	if ipInCIDRs(ip, cidrs) {
		return Result{Check: checkAddress, Status: Pass}
	}
	return Result{
		Check:   checkAddress,
		Status:  Warn,
		Message: fmt.Sprintf("Database address %s is outside the instance VPC (%s, %s).", ip, inst.VPCID, strings.Join(cidrs, ", ")),
		Fix:     fmt.Sprintf("Check %s in config.yaml.", configKey(tc)),
	}
}
