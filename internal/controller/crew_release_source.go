/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Events of publishing a release's source.
const (
	reasonSourcePublished        = "SourcePublished"
	reasonSourceNotGoInstallable = "SourceNotGoInstallable"
)

// maxGoMod bounds the go.mod read for its module path.
const maxGoMod = 1 << 20

// publishSource is the folder the Crew publishes its releases' source to, or
// nil when it publishes none.
func publishSource(crew *c4v1beta1.Crew) *model.PublishSource {
	spec := crew.Spec.PublishReleases
	if spec == nil || spec.Source == nil {
		return nil
	}
	return &model.PublishSource{Path: spec.Source.Path, TagPrefix: spec.Source.TagPrefix}
}

// sourceOverlaps names the other Crews, in any namespace, publishing source to
// the Crew's repository in a folder overlapping its own.
func (r *CrewReconciler) sourceOverlaps(ctx context.Context, crew *c4v1beta1.Crew) ([]string, error) {
	var crews c4v1beta1.CrewList
	if err := r.List(ctx, &crews); err != nil {
		return nil, err
	}
	var places []model.SourcePlace
	for _, c := range crews.Items {
		if p := c.Spec.PublishReleases; p != nil && p.Source != nil {
			places = append(places, model.SourcePlace{Owner: c.Namespace + "/" + c.Name,
				Repository: p.GitHub.Repository, Path: p.Source.Path})
		}
	}
	spec := crew.Spec.PublishReleases
	return model.SourceOverlaps(model.SourcePlace{Owner: crew.Namespace + "/" + crew.Name,
		Repository: spec.GitHub.Repository, Path: spec.Source.Path}, places), nil
}

// readSource reads the Container's files at tag from Gitea and hashes them
// into the release's source, the factory's own on its CI system left out; it
// reads the module path from the root go.mod. Each file is scanned for
// secrets as it streams, known among them, and the files kept are checked
// (model.PublishChecks) into the source's Problems.
func readSource(ctx context.Context, c client.Reader, container *c4v1beta1.Container,
	provider ports.GitHost, repo ports.Repository, tag string, known []string) (*model.ReleaseSource, error) {
	ci, err := containerCI(ctx, c, container)
	if err != nil {
		return nil, err
	}
	var files []model.SourceFile
	var checked []model.CheckedFile
	module := ""
	err = provider.ReadSource(ctx, repo, tag, func(f ports.SourceFile, content io.Reader) error {
		file := model.SourceFile{Path: f.Path, Mode: model.FileMode(f.Mode), Size: f.Size}
		var scan model.SecretScan
		var err error
		if f.Path == "go.mod" && f.Mode != ports.SourceSymlink && f.Size <= maxGoMod {
			var data []byte
			if data, err = io.ReadAll(content); err != nil {
				return err
			}
			module = modfile.ModulePath(data)
			file.Blob = model.BlobSHA(data)
			if scan, err = model.ScanSecrets(f.Path, bytes.NewReader(data), known); err != nil {
				return err
			}
		} else if file.Blob, scan, err = model.ScanBlob(f.Path, content, f.Size, known); err != nil {
			return fmt.Errorf("reading %s: %w", f.Path, err)
		}
		files = append(files, file)
		checked = append(checked, model.CheckedFile{Path: f.Path, Size: f.Size, Secrets: scan})
		return nil
	})
	if err != nil {
		return nil, err
	}
	source, err := model.NewSource(ci, files, module)
	if err != nil {
		return nil, err
	}
	source.Check(checked)
	return source, nil
}

// knownCredentials are the values of the credentials the operator holds for
// the Container's project, which its published source must not hold: the
// GitProvider's token, the Container's agent Git token, the Agents' token
// Secrets in its namespace, and extra, the publish target's own. They are
// compared, never stored or logged. A Secret not there holds nothing.
func knownCredentials(ctx context.Context, c client.Reader, apiReader client.Reader,
	container *c4v1beta1.Container, extra ...string) ([]string, error) {
	known := slices.Clone(extra)
	ns := container.Namespace
	var gp c4v1beta1.GitProvider
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: container.Spec.GitProviderRef.Name}, &gp); err != nil {
		return nil, fmt.Errorf("GitProvider %s: %w", container.Spec.GitProviderRef.Name, err)
	}
	creds, _, err := readCredentials(ctx, apiReader, &gp)
	if err != nil {
		return nil, err
	}
	known = append(known, creds.Token)
	// The rest of the provider's Secret is the host's own: a key that names a
	// credential, such as the private key that mints the agents' tokens,
	// holds one.
	for key, value := range creds.Rest {
		if model.CredentialKey(key) {
			known = append(known, string(value))
		}
	}
	// The agent Git Secret's other keys, its user and scopes, are no secret.
	agentGit, err := secretData(ctx, apiReader, ns, agentGitSecret(container.Name))
	if err != nil {
		return nil, err
	}
	known = append(known, string(agentGit["token"]))
	var agents c4v1beta1.AgentList
	if err := c.List(ctx, &agents, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	var read []string
	for _, a := range agents.Items {
		ref := a.Spec.CredentialsSecretRef
		if ref == nil || slices.Contains(read, ref.Name) {
			continue
		}
		read = append(read, ref.Name)
		data, err := secretData(ctx, apiReader, ns, ref.Name)
		if err != nil {
			return nil, err
		}
		for k, v := range data {
			// Every key is an environment variable: a setting is no secret.
			if model.CredentialKey(k) {
				known = append(known, string(v))
			}
		}
	}
	for i := range known {
		known[i] = strings.TrimSpace(known[i])
	}
	return known, nil
}

// secretData is the data of Secret name in ns, nil when it isn't there.
func secretData(ctx context.Context, apiReader client.Reader, ns, name string) (map[string][]byte, error) {
	var secret corev1.Secret
	err := apiReader.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &secret)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading Secret %s: %w", name, err)
	}
	return secret.Data, nil
}

// sourceRead is a release's source as read from Gitea at its tag.
type sourceRead struct {
	tag    string
	source *model.ReleaseSource
}

// publishFailure is a failed GitHub call as the rule reads it: a branch that
// moved on is no failure, the next poll builds on it.
func publishFailure(err error) *model.PublishFailure {
	if errors.Is(err, ports.ErrNotFastForward) {
		return &model.PublishFailure{Reason: model.PublishBranchMoved, Detail: err.Error()}
	}
	return &model.PublishFailure{Reason: ports.ReleaseReason(err), Detail: err.Error()}
}
