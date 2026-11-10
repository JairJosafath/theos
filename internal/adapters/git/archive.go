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

package git

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// ErrArchive is in every error ReadArchive makes of the archive itself, so a
// caller can tell it from an error of its own fn and say whose archive it is.
var ErrArchive = errors.New("the archive can't be read as a repository's source")

// ReadArchive calls fn with each file of files and its content, in the
// archive's order: the zip a Git host makes of a repository at a ref with git
// archive, whose files all sit below one top directory. The content is
// streamed from the archive, never held whole in memory, and is read only
// during the call. Folders are left out: git keeps none of its own. The files
// are what git archive makes of the ref's tree, so the tree's .gitattributes
// apply: export-ignore leaves a file out, export-subst rewrites its content.
// A submodule fails the read, as its commit can't be read from the archive.
// An error from fn stops the read and is returned as it is; an error of the
// archive wraps ErrArchive.
func ReadArchive(ctx context.Context, files []*zip.File, fn func(ports.SourceFile, io.Reader) error) error {
	if sub := archiveSubmodule(files); sub != "" {
		return fmt.Errorf("%w: %s is a submodule, which the source can't hold", ErrArchive, sub)
	}
	for _, f := range files {
		file, ok, err := archiveFile(f)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrArchive, err)
		}
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := readArchiveFile(f, file, fn); err != nil {
			return err
		}
	}
	return nil
}

// archiveFile says what f, a file of a repository archive whose files all sit
// below one top directory, is in the repository; false for a folder.
func archiveFile(f *zip.File) (ports.SourceFile, bool, error) {
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
	file := ports.SourceFile{Path: name, Mode: ports.SourcePlain, Size: int64(f.UncompressedSize64)} //nolint:gosec // a zip entry's size
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

// archiveSubmodule returns the first folder of files with nothing below it,
// or "". git keeps no empty folder, so such a folder is a submodule, which git
// archive writes as an empty folder.
func archiveSubmodule(files []*zip.File) string {
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

// readArchiveFile opens f's content and hands it to fn as file, closing it after.
func readArchiveFile(f *zip.File, file ports.SourceFile, fn func(ports.SourceFile, io.Reader) error) error {
	content, err := f.Open()
	if err != nil {
		return fmt.Errorf("%w: opening %s: %w", ErrArchive, file.Path, err)
	}
	defer func() { _ = content.Close() }()
	return fn(file, content)
}
