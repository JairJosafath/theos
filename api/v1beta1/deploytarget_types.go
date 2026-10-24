/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeployTier is how much a DeployTarget matters: a production target needs a
// person's approval unless its approval says None.
// +kubebuilder:validation:Enum=development;production
type DeployTier string

// Deploy tiers.
const (
	DeployTierDevelopment DeployTier = "development"
	DeployTierProduction  DeployTier = "production"
)

// DeployApproval says whether a person lets each version into a DeployTarget.
// +kubebuilder:validation:Enum=Required;None
type DeployApproval string

// Deploy approvals.
const (
	DeployApprovalRequired DeployApproval = "Required"
	DeployApprovalNone     DeployApproval = "None"
)

// DeployTargetSpec is a place Containers are shipped to: exactly one member,
// keyed by field name, says which tool owns the place and where in it. Each
// member is added with the adapter that ships to it (docs/plans/done/deploy.md).
// +kubebuilder:validation:XValidation:rule="[has(self.github), has(self.gitlab), has(self.argoCD), has(self.cloudFormation)].exists_one(x, x)",message="set exactly one target member: github, gitlab, argoCD or cloudFormation"
// +kubebuilder:validation:XValidation:rule="has(self.credentialsSecretRef) || has(self.argoCD)",message="set credentialsSecretRef unless the member is argoCD"
type DeployTargetSpec struct {
	// github ships each release as a GitHub release of a repository: its tag,
	// notes and files.
	// +optional
	GitHub *DeployTargetGitHub `json:"github,omitempty"`

	// gitlab ships each release as a GitLab release of a project, on
	// gitlab.com or a self-hosted GitLab: its tag, notes and files, the files
	// in the project's generic package registry (docs/plans/gitlab-deploy.md).
	// +optional
	GitLab *DeployTargetGitLab `json:"gitlab,omitempty"`

	// argoCD ships each release of a Container with the k8s-manifests
	// Deployment as one Argo CD Application, in the cluster the operator runs
	// in or, with a kubeconfig in the Secret, another one: the Container's
	// repository at the release tag, its deploy/k8s path and the release's
	// image; Argo CD rolls it out.
	// +optional
	ArgoCD *DeployTargetArgoCD `json:"argoCD,omitempty"`

	// cloudFormation ships each release of an AWS Container, the CDK
	// assembly its release carries, as CloudFormation stacks in an AWS
	// account and region, through the roles of the account's CDK bootstrap
	// (docs/plans/aws-deploy.md).
	// +optional
	CloudFormation *DeployTargetCloudFormation `json:"cloudFormation,omitempty"`

	// credentialsSecretRef names a Secret in the DeployTarget's namespace that
	// the user creates; its keys are the member's. For github: token, a
	// fine-grained token, or a GitHub App's appId, installationId and
	// privateKey (its PEM), token winning when both are set; packagesToken
	// is optional. For gitlab: token, a project access token with the
	// Maintainer role and the api scope, which covers the container registry
	// (a group or personal access token works the same). For cloudFormation:
	// accessKeyId, secretAccessKey and an optional sessionToken. Only the operator reads it: it is never mounted
	// in a sandbox or passed to a CI job.
	// An argoCD target needs none: the operator uses its own ServiceAccount.
	// Its keys, each optional, are kubeconfig (the cluster Argo CD runs in,
	// when that is not the operator's) and registryUsername and
	// registryPassword (the login to argoCD.imageRegistry for the copy, which
	// reads tags as well as pushes).
	// +optional
	CredentialsSecretRef *LocalRef `json:"credentialsSecretRef,omitempty"`

	// tier is development or production.
	// +kubebuilder:default=development
	// +optional
	Tier DeployTier `json:"tier,omitempty"`

	// approval is Required when a person lets each version in, None when every
	// release ships by itself. Empty means Required for a production target and
	// None for a development one.
	// +optional
	Approval DeployApproval `json:"approval,omitempty"`

	// parameters are deploy-time values that differ per place and are not
	// secret (an alert email, a certificate ARN, a domain), mapped by the
	// member's adapter. A secret value goes in the Secret, never here.
	// +kubebuilder:validation:MaxProperties=50
	// +optional
	Parameters map[string]string `json:"parameters,omitempty"`
}

// DeployTargetGitHub is a GitHub repository whose releases are the deploys.
type DeployTargetGitHub struct {
	// url is a GitHub Enterprise Server's address, https://host, for a
	// repository there; unset, the repository is on github.com. The
	// operator calls its API under /api/v3 and copies images to its
	// registry at containers.<host>, which needs the server's subdomain
	// isolation on.
	// +kubebuilder:validation:MaxLength=200
	// +kubebuilder:validation:XValidation:rule="self.matches('^https://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$')",message="url must be https://host, with no path: the server's address, not its API's"
	// +kubebuilder:validation:XValidation:rule="!self.lowerAscii().matches('^https://(www[.]|api[.])?github[.]com(:443)?$')",message="url is for GitHub Enterprise Server; leave it unset for github.com"
	// +optional
	URL string `json:"url,omitempty"`

	// repository is owner/name, on github.com or the server at url.
	// +kubebuilder:validation:MaxLength=140
	// +kubebuilder:validation:XValidation:rule="self.matches('^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$')",message="repository must be owner/name"
	// +required
	Repository string `json:"repository"`

	// tagPrefix goes before the version in each release's tag. Unset, it is
	// the Container's name and "-" (orders-cli-v0.1.0), so any number of
	// Containers share the target; set, it is used as it is for every
	// Container shipping there, "" giving bare tags (v0.1.0) on a
	// repository of one app.
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._/-]*$`
	// +kubebuilder:validation:XValidation:rule="!self.matches('^[./-]|//|/[.]|[.][.]|[.]lock(/|$)')",message="tagPrefix must make a git tag name: no leading '.', '/' or '-', no '..', '//' or '/.', and no folder ending in .lock"
	// +optional
	TagPrefix *string `json:"tagPrefix,omitempty"`

	// latest says whether each release shipped becomes the repository's
	// Latest release on GitHub. It defaults to true; set it to false on a
	// target for every app but the one people install, when several ship to
	// one repository, so a library's release doesn't take Latest from it.
	// +optional
	Latest *bool `json:"latest,omitempty"`

	// source publishes each release's source too: the app's files at the
	// release's tag, the factory's own left out, committed into a folder of
	// the repository and tagged, with the GitHub release made of that commit.
	// Unset, no source is published.
	// +optional
	Source *DeployTargetSource `json:"source,omitempty"`
}

// DeployTargetSource is where a github or gitlab target publishes each
// release's source.
type DeployTargetSource struct {
	// path is the folder, such as orders-cli; "." is the whole repository.
	// Unset, it is the Container's name, so any number of Containers share
	// the target. The folder is the app's: each release replaces what it
	// holds, so two Containers shipping to the target need folders apart,
	// neither inside the other.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^([.]|[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*)$`
	// +kubebuilder:validation:XValidation:rule="self == '.' || !self.matches('(^|/)([.][.]?|[.][gG][iI][tT])(/|$)')",message="path must have no '.', '..' or '.git' folder in it"
	// +optional
	Path *string `json:"path,omitempty"`

	// tagPrefix goes before the version in the source's tag. Unset, it is
	// the folder and "/" (orders-cli/v0.1.0, the tag go install looks for),
	// and nothing for ".".
	// +kubebuilder:validation:MaxLength=101
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._/-]*$`
	// +kubebuilder:validation:XValidation:rule="!self.matches('^[./-]|//|/[.]|[.][.]|[.]lock(/|$)')",message="tagPrefix must make a git tag name: no leading '.', '/' or '-', no '..', '//' or '/.', and no folder ending in .lock"
	// +optional
	TagPrefix *string `json:"tagPrefix,omitempty"`
}

// DeployTargetGitLab is a GitLab project whose releases are the deploys
// (docs/plans/gitlab-deploy.md, decision 1).
type DeployTargetGitLab struct {
	// url is a self-hosted GitLab's address, https://host[:port], with a
	// path when the instance runs under a relative URL root
	// (https://example.com/gitlab); unset, the project is on gitlab.com. The
	// operator calls its API under /api/v4.
	// +kubebuilder:validation:MaxLength=200
	// +kubebuilder:validation:XValidation:rule="self.matches('^https://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$')",message="url must be https://host[:port], with at most the instance's relative URL root as its path: the instance's address, not its API's"
	// +kubebuilder:validation:XValidation:rule="!self.lowerAscii().matches('^https://(www[.])?gitlab[.]com(:443)?/?$')",message="url is for a self-hosted GitLab; leave it unset for gitlab.com"
	// +optional
	URL string `json:"url,omitempty"`

	// project is the project's full path, group/project or deeper
	// (group/sub/project), on gitlab.com or the instance at url.
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="self.matches('^[A-Za-z0-9_][A-Za-z0-9_.-]*(/[A-Za-z0-9_][A-Za-z0-9_.-]*)+$')",message="project must be the project's full path, group/project or deeper"
	// +required
	Project string `json:"project"`

	// tagPrefix goes before the version in each release's tag. Unset, it is
	// the Container's name and "-" (orders-cli-v0.1.0), so any number of
	// Containers share the target; set, it is used as it is for every
	// Container shipping there, "" giving bare tags (v0.1.0) on a project of
	// one app.
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._/-]*$`
	// +kubebuilder:validation:XValidation:rule="!self.matches('^[./-]|//|/[.]|[.][.]|[.]lock(/|$)')",message="tagPrefix must make a git tag name: no leading '.', '/' or '-', no '..', '//' or '/.', and no folder ending in .lock"
	// +optional
	TagPrefix *string `json:"tagPrefix,omitempty"`

	// source publishes each release's source too: the app's files at the
	// release's tag, the factory's own left out, committed into a folder of
	// the project and tagged, with the GitLab release made of that commit.
	// Unset, no source is published. A gitlab target has no latest: GitLab's
	// latest release is simply the newest.
	// +optional
	Source *DeployTargetSource `json:"source,omitempty"`
}

// DeployTargetCloudFormation is an AWS account and region whose
// CloudFormation stacks are the deploys. No role is named: the roles are
// the CDK bootstrap's, and the release's assembly names them.
type DeployTargetCloudFormation struct {
	// region is the AWS region the stacks run in, such as eu-west-1.
	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z]{2}(-[a-z]+)+-[0-9]+$')",message="region must be an AWS region, such as eu-west-1"
	// +required
	Region string `json:"region"`
	// account is the twelve-digit AWS account id the credential must be
	// in: set, a credential of another account is refused, so a key for the
	// wrong account never deploys.
	// +kubebuilder:validation:MaxLength=12
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9]{12}$')",message="account must be a twelve-digit AWS account id"
	// +optional
	Account string `json:"account,omitempty"`
}

// DeployTargetArgoCD is an Argo CD whose Applications are the deploys, one
// per Container: in the operator's cluster, or in the one the Secret's
// kubeconfig names.
// +kubebuilder:validation:XValidation:rule="!(has(self.imageRegistryInsecure) && self.imageRegistryInsecure) || has(self.imageRegistry)",message="imageRegistryInsecure needs imageRegistry"
// +kubebuilder:validation:XValidation:rule="!has(self.imageRepositoryLayout) || has(self.imageRegistry)",message="imageRepositoryLayout needs imageRegistry"
type DeployTargetArgoCD struct {
	// namespace is where Argo CD runs and its Applications live.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:default=argocd
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// project is the Argo CD project the Applications belong to; it exists
	// before the target is Ready.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:default=default
	// +optional
	Project string `json:"project,omitempty"`

	// destination is where the apps run.
	// +required
	Destination DeployTargetArgoCDDestination `json:"destination"`

	// imageRegistry is a registry the destination's nodes pull from,
	// host[:port][/path], as the nodes see it. Set, each release's image is
	// copied there, under the image's own repository after path, and the
	// Application names the copy; unset, it names the image in the owner's
	// registry.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)*$`
	// +optional
	ImageRegistry string `json:"imageRegistry,omitempty"`

	// imageRegistryInsecure speaks plain HTTP to imageRegistry, for a lab
	// registry without a certificate; unset, the copy uses TLS (#1770).
	// +optional
	ImageRegistryInsecure bool `json:"imageRegistryInsecure,omitempty"`

	// imageRepositoryLayout is where the copy goes below imageRegistry's
	// path: nested, the default, keeps the image's own repository,
	// <owner>/<name>, as the in-cluster Gitea registry takes it; flat keeps
	// only <name>, for a registry that refuses a third path segment, such
	// as Docker Hub (#1770).
	// +kubebuilder:validation:Enum=nested;flat
	// +optional
	ImageRepositoryLayout string `json:"imageRepositoryLayout,omitempty"`

	// gitURL is the Git host's address, scheme://host[:port], as Argo CD
	// sees it. Set, it replaces the scheme and host of the repository's
	// clone URL in each Application; unset, Argo CD clones the URL the
	// operator does.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self.matches('^https?://[A-Za-z0-9.-]+(:[0-9]+)?/?$')",message="gitURL must be scheme://host[:port]"
	// +optional
	GitURL string `json:"gitURL,omitempty"`
}

// DeployTargetArgoCDDestination is where an argoCD target's apps run, as
// Argo CD names it.
type DeployTargetArgoCDDestination struct {
	// namespace is the namespace the apps' manifests go in.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +required
	Namespace string `json:"namespace"`

	// server is the cluster, as Argo CD names it; the default is the
	// cluster Argo CD runs in.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:default="https://kubernetes.default.svc"
	// +optional
	Server string `json:"server,omitempty"`
}

// ConditionDeployTargetReady on a DeployTarget says whether its Secret holds
// the member's keys and the target accepts the credential; for an argoCD
// target without a Secret, whether Argo CD and its project are there. Its reasons are
// Ready, SecretMissing, SecretIncomplete, CredentialRefused and Unreachable.
const ConditionDeployTargetReady = "Ready"

// DeployTargetStatus is what the operator last found of the target.
type DeployTargetStatus struct {
	// conditions: Ready, from the member's check of the credential.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// checkedTime is when the operator last asked the target whether it
	// accepts the credential.
	// +optional
	CheckedTime *metav1.Time `json:"checkedTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=".spec.github.repository"
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=".spec.cloudFormation.region",priority=1
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=".spec.tier"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].reason"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// DeployTarget is a place Containers run, with the credential the operator
// ships to it with, its tier and whether a person approves each version.
type DeployTarget struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DeployTarget
	// +required
	Spec DeployTargetSpec `json:"spec"`

	// status defines the observed state of DeployTarget
	// +optional
	Status DeployTargetStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DeployTargetList contains a list of DeployTarget
type DeployTargetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DeployTarget `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DeployTarget{}, &DeployTargetList{})
		return nil
	})
}
