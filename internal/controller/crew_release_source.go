/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"

	"golang.org/x/mod/modfile"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
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
func publishSource(crew *c4v1alpha1.Crew) *model.PublishSource {
	spec := crew.Spec.PublishReleases
	if spec == nil || spec.Source == nil {
		return nil
	}
	return &model.PublishSource{Path: spec.Source.Path, TagPrefix: spec.Source.TagPrefix}
}

// sourceOverlaps names the other Crews, in any namespace, publishing source to
// the Crew's repository in a folder overlapping its own.
func (r *CrewReconciler) sourceOverlaps(ctx context.Context, crew *c4v1alpha1.Crew) ([]string, error) {
	var crews c4v1alpha1.CrewList
	if err := r.List(ctx, &crews); err != nil {
		return nil, err
	}
	var places []model.SourcePlace
	for _, c := range crews.Items {
		if p := c.Spec.PublishReleases; p != nil && p.Source != nil {
			places = append(places, model.SourcePlace{Crew: c.Namespace + "/" + c.Name,
				Repository: p.GitHub.Repository, Path: p.Source.Path})
		}
	}
	spec := crew.Spec.PublishReleases
	return model.SourceOverlaps(model.SourcePlace{Crew: crew.Namespace + "/" + crew.Name,
		Repository: spec.GitHub.Repository, Path: spec.Source.Path}, places), nil
}

// readSource reads the Container's files at tag from Gitea and hashes them
// into the release's source, the factory's own on its CI system left out; it
// reads the module path from the root go.mod.
func readSource(ctx context.Context, c client.Reader, container *c4v1alpha1.Container,
	provider gitprovider.Provider, repo gitprovider.Repository, tag string) (*model.ReleaseSource, error) {
	ci, err := containerCI(ctx, c, container)
	if err != nil {
		return nil, err
	}
	var files []model.SourceFile
	module := ""
	err = provider.ReadSource(ctx, repo, tag, func(f gitprovider.SourceFile, content io.Reader) error {
		file := model.SourceFile{Path: f.Path, Mode: model.FileMode(f.Mode), Size: f.Size}
		var err error
		if f.Path == "go.mod" && f.Mode != gitprovider.SourceSymlink && f.Size <= maxGoMod {
			var data []byte
			if data, err = io.ReadAll(content); err != nil {
				return err
			}
			module = modfile.ModulePath(data)
			file.Blob = model.BlobSHA(data)
		} else if file.Blob, err = model.HashBlob(content, f.Size); err != nil {
			return fmt.Errorf("reading %s: %w", f.Path, err)
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return model.NewSource(ci, files, module)
}

// sourceRead is a release's source as read from Gitea at its tag.
type sourceRead struct {
	tag    string
	source *model.ReleaseSource
}

// publishFailure is a failed GitHub call as the rule reads it: a branch that
// moved on is no failure, the next poll builds on it.
func publishFailure(err error) *model.PublishFailure {
	if errors.Is(err, gitprovider.ErrNotFastForward) {
		return &model.PublishFailure{Reason: model.PublishBranchMoved, Detail: err.Error()}
	}
	return &model.PublishFailure{Reason: gitprovider.ReleaseReason(err), Detail: err.Error()}
}
