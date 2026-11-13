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

package gitea

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/JairJosafath/theos/internal/adapters/git"
	"github.com/JairJosafath/theos/internal/ports"
)

// maxSourceArchive bounds the archive ReadSource downloads. The archive goes to
// the operator's /tmp, a 1 GiB volume it shares with PublishGoModule's archive
// and module zip; past that the pod is evicted rather than the read failing.
const maxSourceArchive = 256 << 20

// ReadSource reads the files of repo as of tag from Gitea's zip archive of the
// tag, downloaded to a temporary file, and calls fn with each file and its
// content, in the archive's order (git.ReadArchive, which the GitHub adapter
// shares). The content is streamed from the archive, never held whole in
// memory, and is read only during the call. A submodule fails the read, as
// its commit can't be read from the archive. An error from fn stops the read
// and is returned.
func (g *gitea) ReadSource(ctx context.Context, repo ports.Repository, tag string,
	fn func(ports.SourceFile, io.Reader) error) error {
	archive, err := g.downloadArchive(ctx, repo, tag, maxSourceArchive)
	if err != nil {
		return err
	}
	defer removeTemp(archive)
	info, err := archive.Stat()
	if err != nil {
		return err
	}
	files, err := zip.NewReader(archive, info.Size())
	if err != nil {
		return fmt.Errorf("gitea: reading the archive of %s at %s: %w", repoPath(repo), tag, err)
	}
	if err := git.ReadArchive(ctx, files.File, fn); errors.Is(err, git.ErrArchive) {
		return fmt.Errorf("gitea: the archive of %s at %s: %w", repoPath(repo), tag, err)
	} else if err != nil {
		return err
	}
	return nil
}
