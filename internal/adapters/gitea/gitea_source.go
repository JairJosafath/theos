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
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// maxSourceArchive bounds the archive ReadSource downloads. The archive goes to
// the operator's /tmp, a 1 GiB volume it shares with PublishGoModule's archive
// and module zip; past that the pod is evicted rather than the read failing.
const maxSourceArchive = 256 << 20

// ReadSource reads the files of repo as of tag from Gitea's zip archive of the
// tag, downloaded to a temporary file, and calls fn with each file and its
// content, in the archive's order. The content is streamed from the archive,
// never held whole in memory, and is read only during the call. Folders are
// left out: git keeps none of its own. The files are what git archive makes of
// the tag's tree, so the tree's .gitattributes apply: export-ignore leaves a
// file out, export-subst rewrites its content. A submodule fails the read, as
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
	if sub := submodule(files.File); sub != "" {
		return fmt.Errorf("gitea: the archive of %s at %s: %s is a submodule, which the source can't hold",
			repoPath(repo), tag, sub)
	}
	for _, f := range files.File {
		file, ok, err := sourceFile(f)
		if err != nil {
			return fmt.Errorf("gitea: the archive of %s at %s: %w", repoPath(repo), tag, err)
		}
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := readSourceFile(f, file, fn); err != nil {
			return err
		}
	}
	return nil
}

// sourceFile says what f, a file of a repository archive whose files all sit
// below one top directory, is in the repository; false for a folder.
func sourceFile(f *zip.File) (ports.SourceFile, bool, error) {
	mode := f.FileInfo().Mode()
	if mode.IsDir() {
		return ports.SourceFile{}, false, nil
	}
	_, name, ok := strings.Cut(f.Name, "/")
	if !ok || name == "" {
		return ports.SourceFile{}, false, fmt.Errorf("the file %s is not below its top directory", f.Name)
	}
	if !fs.ValidPath(name) || path.Clean(name) != name {
		return ports.SourceFile{}, false, fmt.Errorf("the file %s has a path git can't hold", f.Name)
	}
	file := ports.SourceFile{Path: name, Mode: ports.SourcePlain, Size: int64(f.UncompressedSize64)}
	switch {
	case mode&fs.ModeSymlink != 0:
		file.Mode = ports.SourceSymlink
	case !mode.IsRegular():
		return ports.SourceFile{}, false, fmt.Errorf("the file %s is neither a file nor a symlink (%s)", f.Name, mode)
	case mode&0o111 != 0:
		file.Mode = ports.SourceExecutable
	}
	return file, true, nil
}

// submodule returns the first folder of files with nothing below it, or "".
// git keeps no empty folder, so such a folder is a submodule, which git archive
// writes as an empty folder.
func submodule(files []*zip.File) string {
	empty := map[string]bool{}
	var order []string
	for _, f := range files {
		if f.FileInfo().IsDir() {
			dir := strings.TrimSuffix(f.Name, "/") + "/"
			if _, seen := empty[dir]; !seen {
				empty[dir] = true
				order = append(order, dir)
			}
		}
	}
	for _, f := range files {
		for dir := path.Dir(strings.TrimSuffix(f.Name, "/")); dir != "." && dir != "/"; dir = path.Dir(dir) {
			empty[dir+"/"] = false
		}
	}
	for _, dir := range order {
		if empty[dir] {
			_, name, _ := strings.Cut(strings.TrimSuffix(dir, "/"), "/")
			return name
		}
	}
	return ""
}

// readSourceFile opens f's content and hands it to fn as file, closing it after.
func readSourceFile(f *zip.File, file ports.SourceFile, fn func(ports.SourceFile, io.Reader) error) error {
	content, err := f.Open()
	if err != nil {
		return fmt.Errorf("gitea: opening %s in the archive: %w", file.Path, err)
	}
	defer func() { _ = content.Close() }()
	return fn(file, content)
}
