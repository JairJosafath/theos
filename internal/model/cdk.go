package model

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// A CDK cloud assembly as a release carries it, flat: each file named
// <tier>.<name> (docs/plans/aws-deploy.md, decision 2). The cloudFormation
// member reads it to ship each stack with a change set (decision 7).

// Tags the cloudFormation member sets on each stack it ships, beside the
// assembly's own: the app, namespace/name, the version shipped, the tier,
// so one account can run an app's development and production stacks and
// State tells them apart, and the number of stacks of the assembly, so
// State knows a version runs only once all of them run it.
const (
	CloudFormationTagApp     = "theos.io/app"
	CloudFormationTagVersion = "theos.io/version"
	CloudFormationTagTier    = "theos.io/tier"
	CloudFormationTagStacks  = "theos.io/stacks"
)

// CDK artifact types of a manifest.json.
const (
	cdkArtifactStack  = "aws:cloudformation:stack"
	cdkArtifactAssets = "cdk:asset-manifest"
)

// CDKAssembly is a tier's cloud assembly: its stacks in the order they ship.
type CDKAssembly struct {
	// Stacks are in dependency order, a stack after those it depends on,
	// else by artifact id.
	Stacks []CDKStack
	// BootstrapVersion is the highest bootstrap version a stack requires,
	// and BootstrapVersionParameter the SSM parameter that stack reads it
	// from.
	BootstrapVersion          int
	BootstrapVersionParameter string
}

// CDKStack is one aws:cloudformation:stack artifact of an assembly. Role
// ARNs and the template URL keep their ${AWS::...} placeholders; CDKEnv
// fills them.
type CDKStack struct {
	// ID is the artifact id; StackName the stack's name, properties.stackName
	// else the id.
	ID, StackName string
	// Account and Region are the artifact's environment, "" when unknown
	// (an environment-agnostic stack takes the target's).
	Account, Region string
	// TemplateFile is the template's flat file name.
	TemplateFile string
	// AssumeRoleArn is the deploy role; ExecutionRoleArn the role
	// CloudFormation runs the stack as.
	AssumeRoleArn, ExecutionRoleArn string
	// TemplateObjectURL is the template asset's s3://bucket/key.
	TemplateObjectURL string
	// BootstrapVersion is the bootstrap version the stack requires, read
	// from the SSM parameter BootstrapVersionParameter.
	BootstrapVersion          int
	BootstrapVersionParameter string
	TerminationProtection     bool
	// Tags are the assembly's stack tags.
	Tags map[string]string
	// AssetsFile is the flat name of the stack's asset manifest, "" when
	// none; Assets its file assets.
	AssetsFile string
	Assets     []CDKFileAsset
	// Parameters are the template's parameters, by name.
	Parameters []CDKParameter
}

// CDKFileAsset is a file asset of an asset manifest.
type CDKFileAsset struct {
	// Hash is the asset's id; Path the release file it is (source.path);
	// Packaging "file" or "zip".
	Hash, Path, Packaging string
	// Destinations are where it is published, by destination id.
	Destinations []CDKAssetDestination
}

// CDKAssetDestination is where a file asset is published, with
// placeholders as the manifest has them.
type CDKAssetDestination struct {
	ID, BucketName, ObjectKey, AssumeRoleArn, Region string
}

// CDKParameter is a parameter a template declares.
type CDKParameter struct {
	Name       string
	HasDefault bool
}

// CDKParameterValue is a parameter a change set gives.
type CDKParameterValue struct {
	Name, Value string
}

// CDKManifestFile is the flat name of tier's manifest.json in a release.
func CDKManifestFile(tier string) string {
	return tier + ".manifest.json"
}

type cdkManifest struct {
	Artifacts map[string]struct {
		Type         string          `json:"type"`
		Environment  string          `json:"environment"`
		Dependencies []string        `json:"dependencies"`
		Properties   json.RawMessage `json:"properties"`
	} `json:"artifacts"`
}

type cdkStackProps struct {
	StackName                         string            `json:"stackName"`
	TemplateFile                      string            `json:"templateFile"`
	AssumeRoleArn                     string            `json:"assumeRoleArn"`
	CloudFormationExecutionRoleArn    string            `json:"cloudFormationExecutionRoleArn"`
	StackTemplateAssetObjectURL       string            `json:"stackTemplateAssetObjectUrl"`
	RequiresBootstrapStackVersion     int               `json:"requiresBootstrapStackVersion"`
	BootstrapStackVersionSsmParameter string            `json:"bootstrapStackVersionSsmParameter"`
	TerminationProtection             bool              `json:"terminationProtection"`
	Tags                              map[string]string `json:"tags"`
}

// ParseCDKAssembly reads tier's assembly with read, which returns a release
// file by flat name: the manifest, each stack's template and asset
// manifest. An error of read is wrapped, so errors.Is finds it.
func ParseCDKAssembly(tier string, read func(name string) ([]byte, error)) (CDKAssembly, error) {
	name := CDKManifestFile(tier)
	raw, err := read(name)
	if err != nil {
		return CDKAssembly{}, fmt.Errorf("reading %s: %w", name, err)
	}
	var m cdkManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return CDKAssembly{}, fmt.Errorf("parsing %s: %w", name, err)
	}
	order, err := cdkOrder(m)
	if err != nil {
		return CDKAssembly{}, fmt.Errorf("%s: %w", name, err)
	}
	var a CDKAssembly
	for _, id := range order {
		art := m.Artifacts[id]
		if art.Type != cdkArtifactStack {
			continue
		}
		var p cdkStackProps
		if err := json.Unmarshal(art.Properties, &p); err != nil {
			return CDKAssembly{}, fmt.Errorf("%s: stack %s: %w", name, id, err)
		}
		s := CDKStack{ID: id, StackName: p.StackName, TemplateFile: p.TemplateFile,
			AssumeRoleArn: p.AssumeRoleArn, ExecutionRoleArn: p.CloudFormationExecutionRoleArn,
			TemplateObjectURL: p.StackTemplateAssetObjectURL, BootstrapVersion: p.RequiresBootstrapStackVersion,
			BootstrapVersionParameter: p.BootstrapStackVersionSsmParameter,
			TerminationProtection:     p.TerminationProtection, Tags: p.Tags}
		if s.StackName == "" {
			s.StackName = id
		}
		s.Account, s.Region = cdkEnvironment(art.Environment)
		if s.TemplateFile == "" {
			return CDKAssembly{}, fmt.Errorf("%s: stack %s names no templateFile", name, id)
		}
		tmpl, err := read(s.TemplateFile)
		if err != nil {
			return CDKAssembly{}, fmt.Errorf("reading %s: %w", s.TemplateFile, err)
		}
		if s.Parameters, err = CDKTemplateParameters(tmpl); err != nil {
			return CDKAssembly{}, fmt.Errorf("%s: %w", s.TemplateFile, err)
		}
		for _, dep := range art.Dependencies {
			d, ok := m.Artifacts[dep]
			if !ok || d.Type != cdkArtifactAssets {
				continue
			}
			var ap struct {
				File string `json:"file"`
			}
			if err := json.Unmarshal(d.Properties, &ap); err != nil {
				return CDKAssembly{}, fmt.Errorf("%s: asset manifest %s: %w", name, dep, err)
			}
			if s.AssetsFile != "" || ap.File == "" {
				continue
			}
			s.AssetsFile = ap.File
			raw, err := read(ap.File)
			if err != nil {
				return CDKAssembly{}, fmt.Errorf("reading %s: %w", ap.File, err)
			}
			if s.Assets, err = ParseCDKAssets(raw); err != nil {
				return CDKAssembly{}, fmt.Errorf("%s: %w", ap.File, err)
			}
		}
		if s.BootstrapVersion > a.BootstrapVersion {
			a.BootstrapVersion, a.BootstrapVersionParameter = s.BootstrapVersion, s.BootstrapVersionParameter
		}
		a.Stacks = append(a.Stacks, s)
	}
	if len(a.Stacks) == 0 {
		return CDKAssembly{}, fmt.Errorf("%s has no CloudFormation stack", name)
	}
	return a, nil
}

// cdkOrder is every artifact id of m in dependency order, ties by id; a
// dependency on an artifact m lacks is ignored.
func cdkOrder(m cdkManifest) ([]string, error) {
	needs := map[string]int{}
	users := map[string][]string{}
	for id, art := range m.Artifacts {
		needs[id] += 0
		for _, dep := range art.Dependencies {
			if _, ok := m.Artifacts[dep]; ok && dep != id {
				needs[id]++
				users[dep] = append(users[dep], id)
			}
		}
	}
	var ready, order []string
	for id, n := range needs {
		if n == 0 {
			ready = append(ready, id)
		}
	}
	for len(ready) > 0 {
		slices.Sort(ready)
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, u := range users[id] {
			if needs[u]--; needs[u] == 0 {
				ready = append(ready, u)
			}
		}
	}
	if len(order) != len(needs) {
		return nil, fmt.Errorf("the artifacts' dependencies form a cycle")
	}
	return order, nil
}

// cdkEnvironment splits aws://account/region, "" for unknown parts.
func cdkEnvironment(env string) (account, region string) {
	rest, ok := strings.CutPrefix(env, "aws://")
	if !ok {
		return "", ""
	}
	account, region, _ = strings.Cut(rest, "/")
	if account == "unknown-account" {
		account = ""
	}
	if region == "unknown-region" {
		region = ""
	}
	return account, region
}

// ParseCDKAssets reads an asset manifest's file assets, by hash. Docker
// image assets are refused: the factory doesn't ship them yet.
func ParseCDKAssets(raw []byte) ([]CDKFileAsset, error) {
	var m struct {
		Files map[string]struct {
			Source struct {
				Path      string `json:"path"`
				Packaging string `json:"packaging"`
			} `json:"source"`
			Destinations map[string]struct {
				BucketName    string `json:"bucketName"`
				ObjectKey     string `json:"objectKey"`
				AssumeRoleArn string `json:"assumeRoleArn"`
				Region        string `json:"region"`
			} `json:"destinations"`
		} `json:"files"`
		DockerImages map[string]json.RawMessage `json:"dockerImages"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing the asset manifest: %w", err)
	}
	if len(m.DockerImages) > 0 {
		ids := make([]string, 0, len(m.DockerImages))
		for id := range m.DockerImages {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return nil, fmt.Errorf("the assembly has Docker image assets (%s), which the factory doesn't ship yet",
			strings.Join(ids, ", "))
	}
	out := make([]CDKFileAsset, 0, len(m.Files))
	for hash, f := range m.Files {
		if f.Source.Path == "" {
			return nil, fmt.Errorf("file asset %s has no source.path", hash)
		}
		a := CDKFileAsset{Hash: hash, Path: f.Source.Path, Packaging: f.Source.Packaging}
		for id, d := range f.Destinations {
			a.Destinations = append(a.Destinations, CDKAssetDestination{ID: id, BucketName: d.BucketName,
				ObjectKey: d.ObjectKey, AssumeRoleArn: d.AssumeRoleArn, Region: d.Region})
		}
		slices.SortFunc(a.Destinations, func(x, y CDKAssetDestination) int { return strings.Compare(x.ID, y.ID) })
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y CDKFileAsset) int { return strings.Compare(x.Hash, y.Hash) })
	return out, nil
}

// CDKEnv is where an assembly ships: the partition, account and region its
// ${AWS::Partition}, ${AWS::AccountId} and ${AWS::Region} stand for.
type CDKEnv struct {
	Partition, Account, Region string
}

// Substitute fills s's placeholders from e.
func (e CDKEnv) Substitute(s string) string {
	return strings.NewReplacer("${AWS::Partition}", e.Partition, "${AWS::AccountId}", e.Account,
		"${AWS::Region}", e.Region).Replace(s)
}

// S3Object splits an s3://bucket/key URL, its placeholders filled from e.
func (e CDKEnv) S3Object(url string) (bucket, key string, err error) {
	rest, ok := strings.CutPrefix(e.Substitute(url), "s3://")
	if ok {
		bucket, key, ok = strings.Cut(rest, "/")
	}
	if !ok || bucket == "" || key == "" {
		return "", "", fmt.Errorf("%q is not an s3://bucket/key URL", url)
	}
	return bucket, key, nil
}

// TemplateURL is the https URL CloudFormation reads the object key of
// bucket from, in e's region and partition.
func (e CDKEnv) TemplateURL(bucket, key string) string {
	suffix := "amazonaws.com"
	if e.Partition == "aws-cn" {
		suffix = "amazonaws.com.cn"
	}
	return "https://" + bucket + ".s3." + e.Region + "." + suffix + "/" + key
}

// CDKImageURIParameter is the parameter of a stack that runs the release's
// image, aws-fargate-ts's: the factory gives it the image's copy in the
// bootstrap's ECR repository, by digest (docs/plans/aws-deploy.md,
// decision 8).
const CDKImageURIParameter = "ImageUri"

// CDKDefaultQualifier is the bootstrap's qualifier when a stack names none.
const CDKDefaultQualifier = "hnb659fds"

// CDKTakesImage reports whether a stack with params runs the release's image.
func CDKTakesImage(params []CDKParameter) bool {
	return slices.ContainsFunc(params, func(p CDKParameter) bool { return p.Name == CDKImageURIParameter })
}

// CDKQualifier is the bootstrap qualifier of a stack that reads its
// bootstrap version from the SSM parameter versionParameter,
// /cdk-bootstrap/<qualifier>/version; CDKDefaultQualifier for none.
func CDKQualifier(versionParameter string) (string, error) {
	if versionParameter == "" {
		return CDKDefaultQualifier, nil
	}
	rest, ok := strings.CutPrefix(versionParameter, "/cdk-bootstrap/")
	q, ok2 := strings.CutSuffix(rest, "/version")
	if !ok || !ok2 || q == "" || strings.Contains(q, "/") {
		return "", fmt.Errorf("bootstrap version parameter %q is not /cdk-bootstrap/<qualifier>/version",
			versionParameter)
	}
	return q, nil
}

// CDKImageCopy is where a release's image goes for a stack that runs it:
// the bootstrap's ECR repository, tagged the digest's hex, as the bootstrap's
// image-publishing role. The repository's tags are immutable, and a tag
// named by the content always names the same image.
type CDKImageCopy struct {
	// Host is the account's ECR registry in the region; Repository the
	// bootstrap's container-assets repository; Tag the digest's hex.
	Host, Repository, Tag string
	// Digest is the image's digest, the copy's too.
	Digest string
	// Role is the bootstrap's image-publishing role, which writes the copy.
	Role string
	// URI is the copy by digest, the stack's ImageUri.
	URI string
}

// ImageCopy is where image, host/repository@sha256:<hex> as the operator
// pushed it (#1069), goes for a stack of the bootstrap qualifier q in e.
func (e CDKEnv) ImageCopy(image, q string) (CDKImageCopy, error) {
	_, digest, _ := strings.Cut(image, "@")
	hex, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(hex) != 64 || strings.ContainsFunc(hex, func(r rune) bool { return r > 0x7f || !isLowerHex(byte(r)) }) {
		return CDKImageCopy{}, fmt.Errorf("image %q is not host/repository@sha256:<digest>", image)
	}
	suffix := "amazonaws.com"
	if e.Partition == "aws-cn" {
		suffix = "amazonaws.com.cn"
	}
	c := CDKImageCopy{Host: e.Account + ".dkr.ecr." + e.Region + "." + suffix,
		Repository: "cdk-" + q + "-container-assets-" + e.Account + "-" + e.Region, Tag: hex, Digest: digest,
		Role: "arn:" + e.Partition + ":iam::" + e.Account + ":role/cdk-" + q + "-image-publishing-role-" + e.Account +
			"-" + e.Region}
	c.URI = c.Host + "/" + c.Repository + "@" + digest
	return c, nil
}

// CDKTemplateParameters are the parameters a CloudFormation template (JSON)
// declares, by name.
func CDKTemplateParameters(template []byte) ([]CDKParameter, error) {
	var t struct {
		Parameters map[string]map[string]json.RawMessage `json:"Parameters"`
	}
	if err := json.Unmarshal(template, &t); err != nil {
		return nil, fmt.Errorf("parsing the template: %w", err)
	}
	out := make([]CDKParameter, 0, len(t.Parameters))
	for name, p := range t.Parameters {
		_, def := p["Default"]
		out = append(out, CDKParameter{Name: name, HasDefault: def})
	}
	slices.SortFunc(out, func(x, y CDKParameter) int { return strings.Compare(x.Name, y.Name) })
	return out, nil
}

// CDKChangeSetParameters are the values of given, a target's parameters,
// that params, a template's, declare, by name; the others are ignored.
// Missing are the parameters with no default that given lacks.
func CDKChangeSetParameters(params []CDKParameter, given map[string]string) (set []CDKParameterValue,
	missing []string) {
	for _, p := range params {
		if v, ok := given[p.Name]; ok {
			set = append(set, CDKParameterValue{Name: p.Name, Value: v})
		} else if !p.HasDefault {
			missing = append(missing, p.Name)
		}
	}
	return set, missing
}

// CloudFormationStackTags are the tags a change set gives a stack: the
// assembly's, with the app, tier and version the factory ships and the
// number of stacks of the assembly.
func CloudFormationStackTags(assembly map[string]string, app, tier, version string, stacks int) map[string]string {
	out := make(map[string]string, len(assembly)+4)
	maps.Copy(out, assembly)
	out[CloudFormationTagApp] = app
	out[CloudFormationTagTier] = tier
	out[CloudFormationTagVersion] = version
	out[CloudFormationTagStacks] = strconv.Itoa(stacks)
	return out
}

// CloudFormation's limits on a ClientRequestToken and a change set name.
const cfnNameMax = 128

const (
	cdkTokenPrefix     = "c4f-"
	cdkTokenSep        = "--"
	cdkChangeSetPrefix = "theos-"
)

// cdkEncode keeps [A-Za-z0-9] and writes any other byte as -XX, lowercase
// hex: reversible, never "--", never ending in "-".
func cdkEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isAlnum(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "-%02x", c)
		}
	}
	return b.String()
}

func cdkDecode(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case isAlnum(c):
			b.WriteByte(c)
		case c == '-' && i+2 < len(s) && isLowerHex(s[i+1]) && isLowerHex(s[i+2]):
			b.WriteByte(hexVal(s[i+1])<<4 | hexVal(s[i+2]))
			i += 2
		default:
			return "", false
		}
	}
	return b.String(), true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isLowerHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' }

func hexVal(c byte) byte {
	if c <= '9' {
		return c - '0'
	}
	return c - 'a' + 10
}

// CDKClientRequestToken is the ClientRequestToken of the change set
// execution that ships version: "c4f-", version encoded, "--" and suffix,
// which tells executions of one version apart (a rollback ships an old
// version again). The stack's events carry it, so CDKTokenVersion reads
// the version back. A token over CloudFormation's 128 characters, an empty
// version or a suffix that isn't [A-Za-z0-9]+ is an error.
func CDKClientRequestToken(version, suffix string) (string, error) {
	if version == "" {
		return "", fmt.Errorf("no version to put in a ClientRequestToken")
	}
	if suffix == "" || cdkEncode(suffix) != suffix {
		return "", fmt.Errorf("ClientRequestToken suffix %q is not letters and digits", suffix)
	}
	t := cdkTokenPrefix + cdkEncode(version) + cdkTokenSep + suffix
	if len(t) > cfnNameMax {
		return "", fmt.Errorf("version %q is too long for a ClientRequestToken: %d characters, CloudFormation "+
			"takes %d", version, len(t), cfnNameMax)
	}
	return t, nil
}

// CDKTokenVersion is the version a CDKClientRequestToken carries; ok false
// for any other token, such as one cdk deploy or the console made.
func CDKTokenVersion(token string) (version string, ok bool) {
	rest, ok := strings.CutPrefix(token, cdkTokenPrefix)
	if !ok {
		return "", false
	}
	// The encoded version has no "--" and doesn't end in "-", so the first
	// "--" is the separator.
	enc, suffix, ok := strings.Cut(rest, cdkTokenSep)
	if !ok || enc == "" || suffix == "" || cdkEncode(suffix) != suffix {
		return "", false
	}
	v, ok := cdkDecode(enc)
	if !ok || cdkEncode(v) != enc {
		return "", false
	}
	return v, true
}

// CDKChangeSetName is the name of the change set that ships version:
// "theos-" and version encoded. A name over CloudFormation's 128
// characters, or an empty version, is an error.
func CDKChangeSetName(version string) (string, error) {
	if version == "" {
		return "", fmt.Errorf("no version to name a change set for")
	}
	n := cdkChangeSetPrefix + cdkEncode(version)
	if len(n) > cfnNameMax {
		return "", fmt.Errorf("version %q is too long for a change set name: %d characters, CloudFormation "+
			"takes %d", version, len(n), cfnNameMax)
	}
	return n, nil
}

// Reasons of a failed CloudFormation deploy.
const (
	// DeployReasonRolledBack: the stack rolled the version back.
	DeployReasonRolledBack = "RolledBack"
	// DeployReasonRollbackFailed: the stack failed to roll back and needs a
	// person (continue the rollback, or delete a stack never created).
	DeployReasonRollbackFailed = "RollbackFailed"
	// DeployReasonStackFailed: the stack failed with rollback disabled.
	DeployReasonStackFailed = "StackFailed"
	// DeployReasonStackDeleted: the stack is being, or was, deleted.
	DeployReasonStackDeleted = "StackDeleted"
)

// CloudFormationStackFacts are what the cloudFormation member read of one
// of an app's stacks.
type CloudFormationStackFacts struct {
	Name string
	// Status is the stack's StackStatus; StatusReason its reason.
	Status, StatusReason string
	// EventToken is the ClientRequestToken of the newest stack-level event.
	EventToken string
	// VersionTag is the stack's CloudFormationTagVersion tag, StacksTag its
	// CloudFormationTagStacks tag.
	VersionTag, StacksTag string
	// Outputs and Parameters are the stack's, by key.
	Outputs, Parameters map[string]string
}

// CloudFormationStackBusy says CloudFormation is working on a stack in
// status, so Ship starts nothing on it. REVIEW_IN_PROGRESS, a stack with
// only a change set, isn't busy.
func CloudFormationStackBusy(status string) bool {
	return strings.HasSuffix(status, "_IN_PROGRESS") && status != "REVIEW_IN_PROGRESS"
}

// CloudFormationStackTaken says a stack's tags name another app than app,
// so the member leaves it alone. A stack with no app tag is not taken.
func CloudFormationStackTaken(tags map[string]string, app string) bool {
	owner, ok := tags[CloudFormationTagApp]
	return ok && owner != app
}

// cloudFormationStackRun is one stack's deploy by its status.
func cloudFormationStackRun(s CloudFormationStackFacts) DeployRun {
	failed := func(reason string) DeployRun {
		msg := "stack " + s.Name + " is " + s.Status
		if s.StatusReason != "" {
			msg += ": " + s.StatusReason
		}
		return DeployRun{Phase: DeployPhaseFailed, Health: DeployHealthDegraded, Reason: reason, Message: msg}
	}
	st := s.Status
	switch {
	case st == "CREATE_COMPLETE" || st == "UPDATE_COMPLETE" || st == "IMPORT_COMPLETE" ||
		// The update succeeded; CloudFormation only removes old resources.
		st == "UPDATE_COMPLETE_CLEANUP_IN_PROGRESS":
		return DeployRun{Phase: DeployPhaseDeployed, Health: DeployHealthHealthy}
	case strings.HasPrefix(st, "DELETE_"):
		return failed(DeployReasonStackDeleted)
	case strings.HasSuffix(st, "ROLLBACK_FAILED"):
		return failed(DeployReasonRollbackFailed)
	// A rollback, done or under way, means the version failed: Failed at
	// once, so the loop never ships it again.
	case strings.Contains(st, "ROLLBACK_"):
		return failed(DeployReasonRolledBack)
	case st == "CREATE_FAILED" || st == "UPDATE_FAILED":
		return failed(DeployReasonStackFailed)
	case strings.HasSuffix(st, "_IN_PROGRESS"):
		return DeployRun{Phase: DeployPhaseDeploying, Health: DeployHealthProgressing}
	}
	return DeployRun{Phase: DeployPhaseDeploying, Health: DeployHealthUnknown,
		Message: "stack " + s.Name + " is " + st}
}

// CloudFormationDeployState is what the cloudFormation member reports for an
// app's stacks, in the assembly's order, when wanted is the version the
// factory wants (docs/plans/aws-deploy.md, decision 7). A stack's version
// is the one its newest event's token carries, else its version tag, so a
// rolled-back update reports the version that failed; but a stable stack
// (complete, or rolled back to what ran before) whose version tag is wanted
// runs wanted, Deployed and Healthy, so wanting the version a rollback
// returned to is done. A complete stack is Deployed and Healthy, one in
// progress Deploying, a rolled-back, failed or deleted one Failed and
// Degraded with the stack's reason. Of several stacks Failed beats
// Deploying beats Deployed, and stacks that disagree on the version are
// Deploying; the version is the first failed stack's, else the first
// deploying one's, else the first's. Deployed stacks fewer than their
// stacks tag says are Deploying: the assembly's later stacks don't run the
// version yet. REVIEW_IN_PROGRESS stacks, only a change set so far, don't
// count; none at all is the zero state. The URL is the EntryPoint output:
// as is when it starts with http, else after https:// when the stack has a
// CertificateArn parameter, else after http://.
func CloudFormationDeployState(stacks []CloudFormationStackFacts, wanted string) DeployRun {
	var runs []DeployRun
	url := ""
	need := 0
	for _, s := range stacks {
		if s.Status == "" || s.Status == "REVIEW_IN_PROGRESS" {
			continue
		}
		r := cloudFormationStackRun(s)
		switch v, ok := CDKTokenVersion(s.EventToken); {
		case wanted != "" && s.VersionTag == wanted && cloudFormationStackStable(s.Status):
			r = DeployRun{Phase: DeployPhaseDeployed, Health: DeployHealthHealthy, Version: wanted}
		case ok:
			r.Version = v
		default:
			r.Version = s.VersionTag
		}
		runs = append(runs, r)
		if n, err := strconv.Atoi(s.StacksTag); err == nil && s.VersionTag == r.Version && n > need {
			need = n
		}
		if e := s.Outputs["EntryPoint"]; e != "" && url == "" {
			url = cloudFormationURL(e, s.Parameters["CertificateArn"] != "")
		}
	}
	if len(runs) == 0 {
		return DeployRun{}
	}
	out := runs[0]
	for _, r := range runs {
		if r.Phase == DeployPhaseFailed {
			out = r
			break
		}
		if out.Phase == DeployPhaseDeployed && r.Phase == DeployPhaseDeploying {
			out = r
		}
	}
	if out.Phase == DeployPhaseDeployed {
		for _, r := range runs {
			if r.Version != out.Version {
				out.Phase, out.Health = DeployPhaseDeploying, DeployHealthProgressing
				break
			}
		}
	}
	if out.Phase == DeployPhaseDeployed && len(runs) < need {
		out.Phase, out.Health = DeployPhaseDeploying, DeployHealthProgressing
		out.Message = fmt.Sprintf("%d of the assembly's %d stacks run %s", len(runs), need, out.Version)
	}
	out.URL = url
	return out
}

// cloudFormationStackStable says a stack in status runs what its tags say:
// complete, or rolled back to what ran before an update.
func cloudFormationStackStable(status string) bool {
	return cloudFormationStackRun(CloudFormationStackFacts{Status: status}).Phase == DeployPhaseDeployed ||
		status == "UPDATE_ROLLBACK_COMPLETE"
}

func cloudFormationURL(entry string, https bool) string {
	switch {
	case strings.HasPrefix(entry, "http"):
		return entry
	case https:
		return "https://" + entry
	}
	return "http://" + entry
}
