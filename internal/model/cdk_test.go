package model

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const cdkTestdata = "../adapters/cloudformation/testdata"

func cdkReader(framework string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(cdkTestdata, framework, name))
	}
}

// TestParseCDKAssembly reads the trimmed make dist of each AWS Framework at
// both tiers (docs/plans/aws-deploy.md, decision 10).
func TestParseCDKAssembly(t *testing.T) {
	tests := []struct {
		framework, stack, asset string
		// missing are the template's parameters with no default.
		missing []string
	}{
		{framework: "aws-lambda-ts", stack: "price-fn",
			asset: "asset.2ab6994d9e7bc0d45f63553f0bcecd425c25b142dab3ce3cd794b5fce299c553.zip"},
		{framework: "aws-lambda-go", stack: "stock-fn",
			asset: "asset.85825b3856193b481a3f58360b0bf10e8203b4f48649d66966f644c14fdf0f96.zip"},
		{framework: "aws-lambda-py", stack: "tax-fn",
			asset: "asset.7e0a3e4f1ab3012dc7c8fd3f57d0a88f4381af0824d520f863c87cb9bde2942d.zip"},
		{framework: "aws-ec2-ts", stack: "search",
			asset:   "asset.87b7f2ea4a4d42e9cd63132f98629dadf71f29a0d2b72f86fe8339ec1aac22e7.mjs",
			missing: []string{"CertificateArn"}},
	}
	env := CDKEnv{Partition: "aws", Account: "123456789012", Region: "eu-west-1"}
	for _, tt := range tests {
		for _, tier := range []string{DeployTierDevelopment, DeployTierProduction} {
			t.Run(tt.framework+"/"+tier, func(t *testing.T) {
				a, err := ParseCDKAssembly(tier, cdkReader(tt.framework))
				if err != nil {
					t.Fatal(err)
				}
				name := tt.stack
				if tier == DeployTierDevelopment {
					name += "-development"
				}
				if len(a.Stacks) != 1 {
					t.Fatalf("stacks = %d, want 1", len(a.Stacks))
				}
				s := a.Stacks[0]
				if s.ID != name || s.StackName != name || s.Account != "" || s.Region != "" {
					t.Errorf("stack %s %s in %s/%s, want %s in the target's environment", s.ID, s.StackName,
						s.Account, s.Region, name)
				}
				if a.BootstrapVersion != 6 || s.BootstrapVersion != 6 ||
					a.BootstrapVersionParameter != "/cdk-bootstrap/hnb659fds/version" {
					t.Errorf("bootstrap %d %q", a.BootstrapVersion, a.BootstrapVersionParameter)
				}
				if s.TemplateFile != tier+"."+name+".template.json" || s.AssetsFile != tier+"."+name+".assets.json" {
					t.Errorf("files %s %s", s.TemplateFile, s.AssetsFile)
				}
				if s.TerminationProtection != (tier == DeployTierProduction) {
					t.Errorf("terminationProtection = %v at %s", s.TerminationProtection, tier)
				}
				if s.Tags["theos:container"] == "" {
					t.Errorf("tags = %v, want the assembly's", s.Tags)
				}
				role := "arn:aws:iam::123456789012:role/cdk-hnb659fds-%s-role-123456789012-eu-west-1"
				if got := env.Substitute(s.AssumeRoleArn); got != strings.Replace(role, "%s", "deploy", 1) {
					t.Errorf("deploy role %s", got)
				}
				if got := env.Substitute(s.ExecutionRoleArn); got != strings.Replace(role, "%s", "cfn-exec", 1) {
					t.Errorf("cfn-exec role %s", got)
				}
				bucket, key, err := env.S3Object(s.TemplateObjectURL)
				if err != nil || bucket != "cdk-hnb659fds-assets-123456789012-eu-west-1" || !strings.HasSuffix(key, ".json") {
					t.Errorf("template object %s %s %v", bucket, key, err)
				}
				checkCDKAssets(t, env, tt.framework, tt.asset, s, bucket, key)
				_, missing := CDKChangeSetParameters(s.Parameters, nil)
				if !reflect.DeepEqual(missing, tt.missing) {
					t.Errorf("missing parameters = %v, want %v", missing, tt.missing)
				}
			})
		}
	}
}

// checkCDKAssets checks s's file assets: the code at asset and the template
// at bucket/key, each with one destination.
func checkCDKAssets(t *testing.T, env CDKEnv, framework, asset string, s CDKStack, bucket, key string) {
	t.Helper()
	if len(s.Assets) != 2 {
		t.Fatalf("assets = %d, want the code and the template", len(s.Assets))
	}
	paths := map[string]CDKFileAsset{}
	for _, as := range s.Assets {
		paths[as.Path] = as
		if as.Packaging != "file" || len(as.Destinations) != 1 {
			t.Errorf("asset %s: %s, %d destinations", as.Hash, as.Packaging, len(as.Destinations))
		}
	}
	code, tmpl := paths[asset], paths[s.TemplateFile]
	if code.Hash == "" || tmpl.Hash == "" {
		t.Fatalf("asset paths %v", paths)
	}
	if d := tmpl.Destinations[0]; env.Substitute(d.BucketName) != bucket || d.ObjectKey != key ||
		env.Substitute(d.AssumeRoleArn) != "arn:aws:iam::123456789012:role/cdk-hnb659fds-file-publishing-role-123456789012-eu-west-1" {
		t.Errorf("template destination %+v", d)
	}
	if _, err := os.Stat(filepath.Join(cdkTestdata, framework, code.Path)); err != nil {
		t.Errorf("asset file: %v", err)
	}
}

func TestParseCDKAssemblyErrors(t *testing.T) {
	files := map[string]string{
		"production.manifest.json": `{"artifacts":{
			"b":{"type":"aws:cloudformation:stack","environment":"aws://111122223333/us-east-2",
				"dependencies":["b.assets","a"],"properties":{"templateFile":"b.json","stackName":"bee"}},
			"b.assets":{"type":"cdk:asset-manifest","properties":{"file":"b.assets.json"}},
			"a":{"type":"aws:cloudformation:stack","dependencies":["z"],
				"properties":{"templateFile":"a.json","requiresBootstrapStackVersion":21,
				"bootstrapStackVersionSsmParameter":"/p"}},
			"z":{"type":"aws:cloudformation:stack","properties":{"templateFile":"a.json"}}}}`,
		"a.json":        `{"Parameters":{"X":{"Type":"String"},"Y":{"Type":"String","Default":""}}}`,
		"b.json":        `{}`,
		"b.assets.json": `{"files":{},"dockerImages":{}}`,
	}
	read := func(name string) ([]byte, error) {
		if f, ok := files[name]; ok {
			return []byte(f), nil
		}
		return nil, fs.ErrNotExist
	}
	a, err := ParseCDKAssembly(DeployTierProduction, read)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(a.Stacks))
	for _, s := range a.Stacks {
		names = append(names, s.StackName)
	}
	if !reflect.DeepEqual(names, []string{"z", "a", "bee"}) {
		t.Errorf("order %v, want each stack after those it depends on", names)
	}
	if b := a.Stacks[2]; b.Account != "111122223333" || b.Region != "us-east-2" || b.AssetsFile != "b.assets.json" {
		t.Errorf("stack b %+v", b)
	}
	if a.BootstrapVersion != 21 || a.BootstrapVersionParameter != "/p" {
		t.Errorf("bootstrap %d %s", a.BootstrapVersion, a.BootstrapVersionParameter)
	}

	files["b.assets.json"] = `{"dockerImages":{"abc":{}}}`
	if _, err := ParseCDKAssembly(DeployTierProduction, read); err == nil || !strings.Contains(err.Error(), "Docker") {
		t.Errorf("docker image assets: %v", err)
	}
	delete(files, "b.json")
	if _, err := ParseCDKAssembly(DeployTierProduction, read); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing template: %v, want fs.ErrNotExist wrapped", err)
	}
	files["production.manifest.json"] = `{"artifacts":{"a":{"type":"aws:cloudformation:stack","dependencies":["b"]},
		"b":{"type":"aws:cloudformation:stack","dependencies":["a"]}}}`
	if _, err := ParseCDKAssembly(DeployTierProduction, read); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("a cycle: %v", err)
	}
	files["production.manifest.json"] = `{"artifacts":{}}`
	if _, err := ParseCDKAssembly(DeployTierProduction, read); err == nil {
		t.Error("no stack: no error")
	}
}

func TestCDKEnv(t *testing.T) {
	cn := CDKEnv{Partition: "aws-cn", Account: "1", Region: "cn-north-1"}
	if got := cn.Substitute("arn:${AWS::Partition}:iam::${AWS::AccountId}:role/r-${AWS::Region}"); got != "arn:aws-cn:iam::1:role/r-cn-north-1" {
		t.Errorf("Substitute = %s", got)
	}
	if got := cn.TemplateURL("b", "k.json"); got != "https://b.s3.cn-north-1.amazonaws.com.cn/k.json" {
		t.Errorf("aws-cn TemplateURL = %s", got)
	}
	us := CDKEnv{Partition: "aws", Region: "us-east-1"}
	if got := us.TemplateURL("b", "k.json"); got != "https://b.s3.us-east-1.amazonaws.com/k.json" {
		t.Errorf("TemplateURL = %s", got)
	}
	for _, bad := range []string{"https://b/k", "s3://b", "s3:///k", "s3://b/"} {
		if _, _, err := us.S3Object(bad); err == nil {
			t.Errorf("S3Object(%q): no error", bad)
		}
	}
}

// TestCDKImageCopy covers docs/plans/aws-deploy.md's decision 8 (#1610):
// a Fargate stack's image goes to the bootstrap's ECR repository of its
// qualifier, tagged the digest's hex, as the image-publishing role.
func TestCDKImageCopy(t *testing.T) {
	for param, want := range map[string]string{"": CDKDefaultQualifier,
		"/cdk-bootstrap/hnb659fds/version": "hnb659fds", "/cdk-bootstrap/custom1/version": "custom1"} {
		if got, err := CDKQualifier(param); err != nil || got != want {
			t.Errorf("CDKQualifier(%q) = %q, %v, want %q", param, got, err, want)
		}
	}
	for _, bad := range []string{"/cdk-bootstrap//version", "/other/q/version", "/cdk-bootstrap/a/b/version",
		"/cdk-bootstrap/q"} {
		if _, err := CDKQualifier(bad); err == nil {
			t.Errorf("CDKQualifier(%q): no error", bad)
		}
	}
	hex := strings.Repeat("ab", 32)
	image := "gitea.example:3000/shop/orders@sha256:" + hex
	env := CDKEnv{Partition: "aws", Account: "111122223333", Region: "eu-west-1"}
	got, err := env.ImageCopy(image, "hnb659fds")
	if err != nil {
		t.Fatal(err)
	}
	want := CDKImageCopy{Host: "111122223333.dkr.ecr.eu-west-1.amazonaws.com",
		Repository: "cdk-hnb659fds-container-assets-111122223333-eu-west-1", Tag: hex, Digest: "sha256:" + hex,
		Role: "arn:aws:iam::111122223333:role/cdk-hnb659fds-image-publishing-role-111122223333-eu-west-1",
		URI: "111122223333.dkr.ecr.eu-west-1.amazonaws.com/cdk-hnb659fds-container-assets-111122223333-eu-west-1" +
			"@sha256:" + hex}
	if got != want {
		t.Errorf("ImageCopy = %+v, want %+v", got, want)
	}
	cn := CDKEnv{Partition: "aws-cn", Account: "1", Region: "cn-north-1"}
	if got, _ := cn.ImageCopy(image, "q"); got.Host != "1.dkr.ecr.cn-north-1.amazonaws.com.cn" {
		t.Errorf("aws-cn ImageCopy host = %s", got.Host)
	}
	for _, bad := range []string{"", "r/o:v1", "r/o@sha256:abc", "r/o@sha512:" + hex,
		"r/o@sha256:" + strings.ToUpper(hex)} {
		if _, err := env.ImageCopy(bad, "q"); err == nil {
			t.Errorf("ImageCopy(%q): no error", bad)
		}
	}
	if !CDKTakesImage([]CDKParameter{{Name: "A"}, {Name: CDKImageURIParameter}}) ||
		CDKTakesImage([]CDKParameter{{Name: "A"}}) {
		t.Error("CDKTakesImage: wrong")
	}
}

func TestCDKChangeSetParameters(t *testing.T) {
	params := []CDKParameter{{Name: "AlertEmail", HasDefault: true}, {Name: "BootstrapVersion", HasDefault: true},
		{Name: "CertificateArn"}, {Name: "Other"}}
	set, missing := CDKChangeSetParameters(params,
		map[string]string{"CertificateArn": "arn:c", "AlertEmail": "", "Undeclared": "x"})
	want := []CDKParameterValue{{Name: "AlertEmail", Value: ""}, {Name: "CertificateArn", Value: "arn:c"}}
	if !reflect.DeepEqual(set, want) || !reflect.DeepEqual(missing, []string{"Other"}) {
		t.Errorf("set %v missing %v", set, missing)
	}
}

func TestCloudFormationStackTags(t *testing.T) {
	got := CloudFormationStackTags(map[string]string{"theos:container": "search", CloudFormationTagVersion: "old"},
		"ns/search", "production", "v1.0.0", 2)
	want := map[string]string{"theos:container": "search", "theos.io/app": "ns/search",
		"theos.io/tier": "production", "theos.io/version": "v1.0.0", "theos.io/stacks": "2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tags %v", got)
	}
	if !CloudFormationStackTaken(map[string]string{CloudFormationTagApp: "ns/other"}, "ns/search") ||
		CloudFormationStackTaken(map[string]string{CloudFormationTagApp: "ns/search"}, "ns/search") ||
		CloudFormationStackTaken(nil, "ns/search") {
		t.Error("CloudFormationStackTaken")
	}
	for status, busy := range map[string]bool{"UPDATE_IN_PROGRESS": true, "UPDATE_ROLLBACK_IN_PROGRESS": true,
		"REVIEW_IN_PROGRESS": false, "UPDATE_COMPLETE": false} {
		if CloudFormationStackBusy(status) != busy {
			t.Errorf("CloudFormationStackBusy(%s) = %v", status, !busy)
		}
	}
}

func TestCDKToken(t *testing.T) {
	for _, v := range []string{"v1.2.3", "1.0.0-rc.1+build", "-x--y-", ".", "V9_ü", "a-2e"} {
		tok, err := CDKClientRequestToken(v, "0a1b2c3d")
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if !strings.HasPrefix(tok, "c4f-") || strings.Count(tok[len("c4f-"):], "--") != 1 || !cfnPattern(tok, false) {
			t.Errorf("token %s of %q breaks CloudFormation's pattern or has another --", tok, v)
		}
		if got, ok := CDKTokenVersion(tok); !ok || got != v {
			t.Errorf("CDKTokenVersion(%s) = %q, %v, want %q", tok, got, ok, v)
		}
		name, err := CDKChangeSetName(v)
		if err != nil || !cfnPattern(name, true) {
			t.Errorf("CDKChangeSetName(%q) = %s, %v", v, name, err)
		}
	}
	if tok, _ := CDKClientRequestToken("v1.2.3", "ab"); tok != "c4f-v1-2e2-2e3--ab" {
		t.Errorf("token = %s", tok)
	}
	if n, _ := CDKChangeSetName("v1.2.3"); n != "theos-v1-2e2-2e3" {
		t.Errorf("change set name = %s", n)
	}
	long := strings.Repeat(".", 42) // 126 encoded
	if _, err := CDKClientRequestToken(long, "0a1b2c3d"); err == nil {
		t.Error("a token over 128 characters: no error")
	}
	if _, err := CDKChangeSetName(long); err == nil {
		t.Error("a change set name over 128 characters: no error")
	}
	if _, err := CDKClientRequestToken(strings.Repeat("a", 114), "0a1b2c3d"); err != nil {
		t.Errorf("a 128-character token: %v", err)
	}
	for _, bad := range [][2]string{{"", "ab"}, {"v1", ""}, {"v1", "a-b"}} {
		if _, err := CDKClientRequestToken(bad[0], bad[1]); err == nil {
			t.Errorf("CDKClientRequestToken(%q, %q): no error", bad[0], bad[1])
		}
	}
	for _, tok := range []string{"", "cdk-deploy-123", "c4f-v1", "c4f---ab", "c4f-v1--", "c4f-v1-2E--ab",
		"c4f-v1-2--ab", "c4f-v1-61--ab", "Console-abc"} {
		if v, ok := CDKTokenVersion(tok); ok {
			t.Errorf("CDKTokenVersion(%q) = %q, ok", tok, v)
		}
	}
}

// cfnPattern checks CloudFormation's pattern for a ClientRequestToken,
// [a-zA-Z0-9][-a-zA-Z0-9]*, or a change set name, [a-zA-Z][-a-zA-Z0-9]*.
func cfnPattern(s string, name bool) bool {
	if s == "" || len(s) > 128 || s[0] == '-' || name && s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isAlnum(s[i]) && s[i] != '-' {
			return false
		}
	}
	return true
}

// TestCloudFormationDeployState covers docs/plans/aws-deploy.md, decision 7:
// the newest event's token gives the version, the stack status the phase.
func TestCloudFormationDeployState(t *testing.T) {
	tok := func(v string) string {
		s, err := CDKClientRequestToken(v, "0a1b2c3d")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	stack := func(name, status, version string) CloudFormationStackFacts {
		return CloudFormationStackFacts{Name: name, Status: status, EventToken: tok(version)}
	}
	failed := func(v, reason, msg string) DeployRun {
		return DeployRun{Version: v, Phase: DeployPhaseFailed, Health: DeployHealthDegraded, Reason: reason, Message: msg}
	}
	deployed := DeployRun{Version: "v2", Phase: DeployPhaseDeployed, Health: DeployHealthHealthy}
	deploying := DeployRun{Version: "v2", Phase: DeployPhaseDeploying, Health: DeployHealthProgressing}
	tests := []struct {
		name   string
		stacks []CloudFormationStackFacts
		wanted string
		want   DeployRun
	}{
		{name: "no stacks is the zero state"},
		{name: "REVIEW_IN_PROGRESS, only a change set, is the zero state",
			stacks: []CloudFormationStackFacts{stack("s", "REVIEW_IN_PROGRESS", "v2")}},
		{name: "CREATE_COMPLETE is Deployed", stacks: []CloudFormationStackFacts{stack("s", "CREATE_COMPLETE", "v2")},
			want: deployed},
		{name: "UPDATE_COMPLETE is Deployed", stacks: []CloudFormationStackFacts{stack("s", "UPDATE_COMPLETE", "v2")},
			want: deployed},
		{name: "IMPORT_COMPLETE is Deployed", stacks: []CloudFormationStackFacts{stack("s", "IMPORT_COMPLETE", "v2")},
			want: deployed},
		{name: "cleanup after a successful update is Deployed",
			stacks: []CloudFormationStackFacts{stack("s", "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS", "v2")}, want: deployed},
		{name: "CREATE_IN_PROGRESS is Deploying",
			stacks: []CloudFormationStackFacts{stack("s", "CREATE_IN_PROGRESS", "v2")}, want: deploying},
		{name: "UPDATE_IN_PROGRESS is Deploying",
			stacks: []CloudFormationStackFacts{stack("s", "UPDATE_IN_PROGRESS", "v2")}, want: deploying},
		{name: "a rollback under way is Failed at once, at the version that failed",
			stacks: []CloudFormationStackFacts{stack("s", "UPDATE_ROLLBACK_IN_PROGRESS", "v2")},
			want:   failed("v2", DeployReasonRolledBack, "stack s is UPDATE_ROLLBACK_IN_PROGRESS")},
		{name: "UPDATE_ROLLBACK_COMPLETE is the wanted version Failed, not the old one running",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "UPDATE_ROLLBACK_COMPLETE",
				StatusReason: "Resource Fn failed", EventToken: tok("v2"), VersionTag: "v1"}},
			want: failed("v2", DeployReasonRolledBack, "stack s is UPDATE_ROLLBACK_COMPLETE: Resource Fn failed")},
		{name: "ROLLBACK_COMPLETE, a failed first create, is RolledBack",
			stacks: []CloudFormationStackFacts{stack("s", "ROLLBACK_COMPLETE", "v2")},
			want:   failed("v2", DeployReasonRolledBack, "stack s is ROLLBACK_COMPLETE")},
		{name: "ROLLBACK_FAILED is RollbackFailed",
			stacks: []CloudFormationStackFacts{stack("s", "ROLLBACK_FAILED", "v2")},
			want:   failed("v2", DeployReasonRollbackFailed, "stack s is ROLLBACK_FAILED")},
		{name: "UPDATE_ROLLBACK_FAILED is RollbackFailed",
			stacks: []CloudFormationStackFacts{stack("s", "UPDATE_ROLLBACK_FAILED", "v2")},
			want:   failed("v2", DeployReasonRollbackFailed, "stack s is UPDATE_ROLLBACK_FAILED")},
		{name: "CREATE_FAILED is StackFailed",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "CREATE_FAILED", StatusReason: "no",
				EventToken: tok("v2")}},
			want: failed("v2", DeployReasonStackFailed, "stack s is CREATE_FAILED: no")},
		{name: "UPDATE_FAILED is StackFailed",
			stacks: []CloudFormationStackFacts{stack("s", "UPDATE_FAILED", "v2")},
			want:   failed("v2", DeployReasonStackFailed, "stack s is UPDATE_FAILED")},
		{name: "DELETE_IN_PROGRESS is StackDeleted",
			stacks: []CloudFormationStackFacts{stack("s", "DELETE_IN_PROGRESS", "v2")},
			want:   failed("v2", DeployReasonStackDeleted, "stack s is DELETE_IN_PROGRESS")},
		{name: "DELETE_FAILED is StackDeleted",
			stacks: []CloudFormationStackFacts{stack("s", "DELETE_FAILED", "v2")},
			want:   failed("v2", DeployReasonStackDeleted, "stack s is DELETE_FAILED")},
		{name: "an unknown status is Deploying, health Unknown",
			stacks: []CloudFormationStackFacts{stack("s", "NEW_STATUS", "v2")},
			want: DeployRun{Version: "v2", Phase: DeployPhaseDeploying, Health: DeployHealthUnknown,
				Message: "stack s is NEW_STATUS"}},
		{name: "with no c4f token, the version tag gives the version",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "UPDATE_COMPLETE", EventToken: "Console-1",
				VersionTag: "v2"}},
			want: deployed},
		{name: "Failed beats Deploying",
			stacks: []CloudFormationStackFacts{stack("a", "UPDATE_IN_PROGRESS", "v2"),
				stack("b", "UPDATE_ROLLBACK_COMPLETE", "v2")},
			want: failed("v2", DeployReasonRolledBack, "stack b is UPDATE_ROLLBACK_COMPLETE")},
		{name: "Deploying beats Deployed",
			stacks: []CloudFormationStackFacts{stack("a", "UPDATE_COMPLETE", "v2"), stack("b", "UPDATE_IN_PROGRESS", "v2")},
			want:   deploying},
		{name: "stacks that disagree on the version are Deploying, at the first stack's",
			stacks: []CloudFormationStackFacts{stack("a", "UPDATE_COMPLETE", "v2"), stack("b", "UPDATE_COMPLETE", "v1")},
			want:   deploying},
		{name: "#1608: fewer stacks run the version than the assembly has, Deploying",
			stacks: []CloudFormationStackFacts{{Name: "a", Status: "CREATE_COMPLETE", EventToken: tok("v2"),
				VersionTag: "v2", StacksTag: "2"}},
			want: DeployRun{Version: "v2", Phase: DeployPhaseDeploying, Health: DeployHealthProgressing,
				Message: "1 of the assembly's 2 stacks run v2"}},
		{name: "#1608: the second stack only a change set so far, Deploying",
			stacks: []CloudFormationStackFacts{{Name: "a", Status: "CREATE_COMPLETE", EventToken: tok("v2"),
				VersionTag: "v2", StacksTag: "2"}, {Name: "b", Status: "REVIEW_IN_PROGRESS"}},
			want: DeployRun{Version: "v2", Phase: DeployPhaseDeploying, Health: DeployHealthProgressing,
				Message: "1 of the assembly's 2 stacks run v2"}},
		{name: "#1608: every stack of the assembly runs the version, Deployed",
			stacks: []CloudFormationStackFacts{
				{Name: "a", Status: "UPDATE_COMPLETE", EventToken: tok("v2"), VersionTag: "v2", StacksTag: "2"},
				{Name: "b", Status: "CREATE_COMPLETE", EventToken: tok("v2"), VersionTag: "v2", StacksTag: "2"}},
			want: deployed},
		{name: "#1608: rolled back to the wanted version is Deployed at it",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "UPDATE_ROLLBACK_COMPLETE",
				StatusReason: "Resource Fn failed", EventToken: tok("v3"), VersionTag: "v2"}},
			wanted: "v2", want: deployed},
		{name: "#1608: rolled back, wanting the version that failed, is Failed",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "UPDATE_ROLLBACK_COMPLETE",
				EventToken: tok("v2"), VersionTag: "v1"}},
			wanted: "v2", want: failed("v2", DeployReasonRolledBack, "stack s is UPDATE_ROLLBACK_COMPLETE")},
		{name: "#1608: a failed first create tagged the wanted version is still Failed",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "ROLLBACK_COMPLETE", EventToken: tok("v2"),
				VersionTag: "v2"}},
			wanted: "v2", want: failed("v2", DeployReasonRolledBack, "stack s is ROLLBACK_COMPLETE")},
		{name: "an http EntryPoint is the URL as is",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "UPDATE_COMPLETE", EventToken: tok("v2"),
				Outputs: map[string]string{"EntryPoint": "https://abc.lambda-url.eu-west-1.on.aws/"}}},
			want: DeployRun{Version: "v2", Phase: DeployPhaseDeployed, Health: DeployHealthHealthy,
				URL: "https://abc.lambda-url.eu-west-1.on.aws/"}},
		{name: "a host with a CertificateArn is https",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "UPDATE_COMPLETE", EventToken: tok("v2"),
				Outputs:    map[string]string{"EntryPoint": "lb.example.com"},
				Parameters: map[string]string{"CertificateArn": "arn:aws:acm:eu-west-1:1:certificate/x"}}},
			want: DeployRun{Version: "v2", Phase: DeployPhaseDeployed, Health: DeployHealthHealthy,
				URL: "https://lb.example.com"}},
		{name: "a host with no CertificateArn is http",
			stacks: []CloudFormationStackFacts{{Name: "s", Status: "UPDATE_COMPLETE", EventToken: tok("v2"),
				Outputs:    map[string]string{"EntryPoint": "lb.example.com"},
				Parameters: map[string]string{"CertificateArn": ""}}},
			want: DeployRun{Version: "v2", Phase: DeployPhaseDeployed, Health: DeployHealthHealthy,
				URL: "http://lb.example.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CloudFormationDeployState(tt.stacks, tt.wanted); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
