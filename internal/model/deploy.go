package model

import (
	"fmt"
	"slices"
	"strings"
)

// Deploy tiers, as DeployTarget.spec.tier names them; "" is development.
const (
	DeployTierDevelopment = "development"
	DeployTierProduction  = "production"
)

// Deploy approvals, as DeployTarget.spec.approval names them.
const (
	DeployApprovalRequired = "Required"
	DeployApprovalNone     = "None"
)

// DeployApproval is the approval a target with tier and approval needs: its
// approval when set, else Required for a production target and None for any
// other. The rule is here, not in CRD defaulting, so a stored target never
// changes shape.
func DeployApproval(tier, approval string) string {
	if approval != "" {
		return approval
	}
	if tier == DeployTierProduction {
		return DeployApprovalRequired
	}
	return DeployApprovalNone
}

// DeployContainer is what a Container brings that a member may need of it.
type DeployContainer struct {
	// Deployment is the Container's Deployment catalog entry, "" when none.
	Deployment string
	// DeploysItself says the Container's Framework deploys itself.
	DeploysItself bool
}

// DeployNeed is one thing a member needs a Container to have.
type DeployNeed struct {
	// What names it in a message, such as "a Deployment that writes deploy/k8s/".
	What string
	// Met says c has it.
	Met func(c DeployContainer) bool
}

// DeployMember is what a DeployTarget's member means to the factory: the keys
// its Secret holds and what it needs a Container to have. Each member is one
// row in deployMembers, added with its adapter.
type DeployMember struct {
	// Name is the member's field name in DeployTarget.spec, such as github.
	Name string
	// Tool names who owns the place in a message, such as GitHub.
	Tool string
	// SecretKeys are the keys the target's credentialsSecretRef holds.
	SecretKeys []string
	// Holds says what the Secret holds, for a message.
	Holds string
	// Needs are what a Container must have to be shipped there; none for a
	// member that ships the release alone.
	Needs []DeployNeed
}

// DeployMemberGitHub ships each release as a GitHub release: its tag, notes
// and files. A Container needs nothing beyond its releases.
const DeployMemberGitHub = "github"

// Settings of a github target, as its adapter reads them.
const (
	// GitHubSettingRepository is the repository, owner/name.
	GitHubSettingRepository = "repository"
	// GitHubSettingTagPrefix goes before the version in the GitHub tag.
	GitHubSettingTagPrefix = "tagPrefix"
	// GitHubSettingLatest "false" leaves the repository's Latest where it is;
	// any other value makes each release Latest.
	GitHubSettingLatest = "latest"
	// GitHubSettingSourcePath is the folder of the repository the release's
	// source goes in, "." for its root; unset publishes no source.
	GitHubSettingSourcePath = "source.path"
	// GitHubSettingSourceTagPrefix goes before the version in the source's
	// tag: unset is the default (SourceTag), "" gives bare tags.
	GitHubSettingSourceTagPrefix = "source.tagPrefix"
)

// Settings of a cloudFormation target, as its adapter reads them.
const (
	// CloudFormationSettingRegion is the AWS region the stacks ship to.
	CloudFormationSettingRegion = "region"
	// CloudFormationSettingAccount is the AWS account id the credential
	// must be in.
	CloudFormationSettingAccount = "account"
)

// GitHubTagPrefix is the prefix a Container's tags carry at a github target:
// the target's tagPrefix as it is when set ("" giving bare tags), else the
// Container's name and "-", so any number of Containers share a target with
// no setting (docs/plans/github-deploy-state.md, decision 3).
func GitHubTagPrefix(set *string, container string) string {
	if set != nil {
		return *set
	}
	return container + "-"
}

// GitHubTakesLatest says whether a github target's releases become the
// repository's Latest: its latest field, true when unset.
func GitHubTakesLatest(latest *bool) bool {
	return latest == nil || *latest
}

// deployMembers are the members the factory ships to, by name.
var deployMembers = map[string]DeployMember{
	DeployMemberGitHub: {Name: DeployMemberGitHub, Tool: "GitHub", SecretKeys: []string{"token"},
		Holds: "a fine-grained token for the repository alone, with Contents read and write"},
	// An argoCD target has no Secret: the operator's own ServiceAccount
	// writes the Applications (docs/plans/k8s-deploy.md, decision 2).
	DeployMemberArgoCD: {Name: DeployMemberArgoCD, Tool: "Argo CD",
		Needs: []DeployNeed{{What: "a Deployment that writes " + ArgoCDManifestsPath + "/",
			Met: func(c DeployContainer) bool { return c.Deployment == DeploymentK8sManifests }}}},
}

// DeployMemberByName returns the member name names.
func DeployMemberByName(name string) (DeployMember, error) {
	m, ok := deployMembers[name]
	if !ok {
		return DeployMember{}, fmt.Errorf("unknown deploy target member %q", name)
	}
	return m, nil
}

// DeployMemberNames are the members the factory ships to, sorted.
func DeployMemberNames() []string {
	names := make([]string, 0, len(deployMembers))
	for n := range deployMembers {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Lacks returns what c lacks of what m needs, in the table's order; none
// when c can be shipped there.
func (m DeployMember) Lacks(c DeployContainer) []string {
	var out []string
	for _, n := range m.Needs {
		if !n.Met(c) {
			out = append(out, n.What)
		}
	}
	return out
}

// MissingKeys returns the keys of m's Secret that data lacks or holds empty,
// in the table's order.
func (m DeployMember) MissingKeys(data map[string][]byte) []string {
	var out []string
	for _, k := range m.SecretKeys {
		if strings.TrimSpace(string(data[k])) == "" {
			out = append(out, k)
		}
	}
	return out
}

// DeployCheck is what checking a DeployTarget's credential found, for the
// message of its Ready condition.
type DeployCheck struct {
	// Target is the DeployTarget's name, Secret its credentialsSecretRef's,
	// and Where the place in the member's words, such as owner/name.
	Target, Secret, Where string
	Member                DeployMember
	// Missing are the Secret's keys it lacks.
	Missing []string
	// Detail is what the target's tool said, or "".
	Detail string
}

// Ready condition reasons of a DeployTarget.
const (
	DeployTargetReady             = "Ready"
	DeployTargetSecretMissing     = "SecretMissing"
	DeployTargetSecretIncomplete  = "SecretIncomplete"
	DeployTargetCredentialRefused = "CredentialRefused" //nolint:gosec // a reason, not a credential
	DeployTargetUnreachable       = "Unreachable"
)

// DeployCheckMessage is the message of a DeployTarget's Ready condition with
// reason, one of DeployTarget*. A refused credential names the
// target and who sets the Secret, as the release copy's message does.
func DeployCheckMessage(reason string, c DeployCheck) string {
	detail := ""
	if c.Detail != "" {
		detail = ": " + c.Detail
	}
	keys := strings.Join(c.Member.SecretKeys, ", ")
	switch reason {
	case DeployTargetSecretMissing:
		return fmt.Sprintf("Secret %s, with the key %s, is missing; whoever owns DeployTarget %s creates it "+
			"holding %s for %s", c.Secret, keys, c.Target, c.Member.Holds, c.Where)
	case DeployTargetSecretIncomplete:
		return fmt.Sprintf("Secret %s of DeployTarget %s has no %s %s; whoever owns the target sets it to %s for %s",
			c.Secret, c.Target, plural(len(c.Missing), "key", "keys"), strings.Join(c.Missing, ", "),
			c.Member.Holds, c.Where)
	case DeployTargetCredentialRefused:
		if c.Secret == "" {
			return fmt.Sprintf("%s refused the operator's ServiceAccount for DeployTarget %s for %s%s. Whoever owns "+
				"DeployTarget %s makes %s and lets the operator use it", c.Member.Tool, c.Target, c.Where, detail,
				c.Target, c.Where)
		}
		return fmt.Sprintf("%s refused the credential in Secret %s of DeployTarget %s for %s%s. Whoever owns "+
			"DeployTarget %s sets Secret %s to %s for %s", c.Member.Tool, c.Secret, c.Target, c.Where, detail,
			c.Target, c.Secret, c.Member.Holds, c.Where)
	case DeployTargetUnreachable:
		return fmt.Sprintf("%s could not be reached to check DeployTarget %s for %s%s; checked again on the next poll",
			c.Member.Tool, c.Target, c.Where, detail)
	}
	if c.Secret == "" {
		return fmt.Sprintf("%s is there for %s", c.Member.Tool, c.Where)
	}
	return fmt.Sprintf("%s accepts the credential in Secret %s for %s", c.Member.Tool, c.Secret, c.Where)
}
