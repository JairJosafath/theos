package model

import (
	"fmt"
	"net/url"
	"strings"
)

// Settings of an argoCD target on another cluster than the operator's
// (docs/plans/k8s-deploy.md, decision 9).
const (
	// ArgoCDSettingImageRegistry is the registry the cluster's nodes pull
	// from, host[:port][/path], as the nodes see it; set, each release's
	// image is copied there and the Application names the copy.
	ArgoCDSettingImageRegistry = "imageRegistry"
	// ArgoCDSettingGitURL is the Git host's address, scheme://host[:port],
	// as the cluster's Argo CD sees it; set, it replaces the scheme and host
	// of the repository's clone URL in the Application.
	ArgoCDSettingGitURL = "gitURL"
	// ArgoCDSettingImageRegistryInsecure, "true", speaks plain HTTP to
	// imageRegistry; anything else is TLS (#1770).
	ArgoCDSettingImageRegistryInsecure = "imageRegistryInsecure"
	// ArgoCDSettingImageRepositoryLayout is where the copy goes below
	// imageRegistry's path: ArgoCDLayoutNested or ArgoCDLayoutFlat (#1770).
	ArgoCDSettingImageRepositoryLayout = "imageRepositoryLayout"
)

// Layouts of the copy's repository below imageRegistry's path (#1770).
const (
	// ArgoCDLayoutNested, the default, keeps the image's own repository,
	// <owner>/<name>, as the in-cluster Gitea registry takes it.
	ArgoCDLayoutNested = "nested"
	// ArgoCDLayoutFlat keeps only <name>, for a registry that refuses a
	// third path segment, such as Docker Hub.
	ArgoCDLayoutFlat = "flat"
)

// Keys of an argoCD target's Secret, each optional: a target without a
// Secret is the operator's own cluster, pulling the image from the owner's
// registry.
const (
	// ArgoCDSecretKubeconfig is a kubeconfig for the cluster Argo CD runs
	// in; the adapter writes and reads the Applications with it.
	ArgoCDSecretKubeconfig = "kubeconfig"
	// ArgoCDSecretRegistryUsername and ArgoCDSecretRegistryPassword log in
	// to imageRegistry for the copy; both unset is anonymous.
	ArgoCDSecretRegistryUsername = "registryUsername"
	ArgoCDSecretRegistryPassword = "registryPassword"
)

// ArgoCDRepoURL is the repoURL of an Application for the repository whose
// clone URL is cloneURL, at a target whose Git host Argo CD reaches at
// gitURL: cloneURL with gitURL's scheme and host, or cloneURL as it is when
// gitURL is "".
func ArgoCDRepoURL(cloneURL, gitURL string) (string, error) {
	if gitURL == "" {
		return cloneURL, nil
	}
	clone, err := url.Parse(cloneURL)
	if err != nil {
		return "", fmt.Errorf("the repository's clone URL %q: %w", cloneURL, err)
	}
	git, err := url.Parse(gitURL)
	if err != nil || git.Scheme == "" || git.Host == "" || strings.Trim(git.Path, "/") != "" {
		return "", fmt.Errorf("gitURL %q is not scheme://host[:port]", gitURL)
	}
	clone.Scheme, clone.Host, clone.User = git.Scheme, git.Host, nil
	return clone.String(), nil
}

// ArgoCDImageCopy is where a release's image is copied for an argoCD target
// with an imageRegistry, and the reference the Application names.
type ArgoCDImageCopy struct {
	// FromHost and FromRepository are the image's registry and repository
	// in the owner's registry; Digest is its digest, the copy's too.
	FromHost, FromRepository, Digest string
	// ToHost and ToRepository are the copy's registry and repository: the
	// imageRegistry's path, then the image's own repository, or only its
	// name under the flat layout.
	ToHost, ToRepository string
	// Image is the copy's reference by digest, as the nodes pull it.
	Image string
}

// ArgoCDImageCopyOf is where image, host/repository@digest as the operator
// pushed it (#1069), goes at registry, host[:port][/path], laid out as
// layout says: ArgoCDLayoutNested or "" keeps the image's repository,
// ArgoCDLayoutFlat its last segment only (#1770).
func ArgoCDImageCopyOf(image, registry, layout string) (ArgoCDImageCopy, error) {
	ref, digest, ok := strings.Cut(image, "@")
	fromHost, fromRepo, ok2 := strings.Cut(ref, "/")
	if !ok || !ok2 || digest == "" || fromHost == "" || fromRepo == "" {
		return ArgoCDImageCopy{}, fmt.Errorf("image %q is not host/repository@digest", image)
	}
	toHost, prefix, _ := strings.Cut(strings.TrimSuffix(registry, "/"), "/")
	if toHost == "" || strings.Contains(registry, "://") {
		return ArgoCDImageCopy{}, fmt.Errorf("imageRegistry %q is not host[:port][/path]", registry)
	}
	toRepo := fromRepo
	switch layout {
	case "", ArgoCDLayoutNested:
	case ArgoCDLayoutFlat:
		toRepo = fromRepo[strings.LastIndex(fromRepo, "/")+1:]
	default:
		return ArgoCDImageCopy{}, fmt.Errorf("imageRepositoryLayout %q is not %s or %s", layout,
			ArgoCDLayoutNested, ArgoCDLayoutFlat)
	}
	if prefix != "" {
		toRepo = prefix + "/" + toRepo
	}
	return ArgoCDImageCopy{FromHost: fromHost, FromRepository: fromRepo, Digest: digest, ToHost: toHost,
		ToRepository: toRepo, Image: toHost + "/" + toRepo + "@" + digest}, nil
}
