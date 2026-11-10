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
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// entry is one entry of a test archive: a folder when its name ends in "/".
type entry struct {
	name    string
	mode    fs.FileMode
	content string
}

func archive(t *testing.T, entries ...entry) []*zip.File {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if strings.HasSuffix(e.name, "/") {
			mode |= fs.ModeDir | 0o755
		} else if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return r.File
}

// TestReadArchive covers #75: the files of a host's zip of a repository, as
// git archive makes it, are read below its top directory with their modes and
// sizes, folders left out; a submodule, a path outside the top directory and
// a path git can't hold fail the read; fn's error stops it and comes back as
// it is.
func TestReadArchive(t *testing.T) {
	ctx := context.Background()
	files := archive(t,
		entry{name: "acme-app-0123abc/"},
		entry{name: "acme-app-0123abc/README.md", content: "hello\n"},
		entry{name: "acme-app-0123abc/docs/"},
		entry{name: "acme-app-0123abc/docs/a.md", content: "a\n"},
		entry{name: "acme-app-0123abc/run.sh", mode: 0o755, content: "#!/bin/sh\n"},
		entry{name: "acme-app-0123abc/link", mode: fs.ModeSymlink | 0o777, content: "README.md"},
	)
	type read struct {
		file    ports.SourceFile
		content string
	}
	var got []read
	err := ReadArchive(ctx, files, func(f ports.SourceFile, r io.Reader) error {
		data, err := io.ReadAll(r)
		got = append(got, read{f, string(data)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []read{
		{ports.SourceFile{Path: "README.md", Mode: ports.SourcePlain, Size: 6}, "hello\n"},
		{ports.SourceFile{Path: "docs/a.md", Mode: ports.SourcePlain, Size: 2}, "a\n"},
		{ports.SourceFile{Path: "run.sh", Mode: ports.SourceExecutable, Size: 10}, "#!/bin/sh\n"},
		{ports.SourceFile{Path: "link", Mode: ports.SourceSymlink, Size: 9}, "README.md"},
	}
	if len(got) != len(want) {
		t.Fatalf("ReadArchive() read %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	stop := errors.New("stop")
	err = ReadArchive(ctx, files, func(ports.SourceFile, io.Reader) error { return stop })
	if !errors.Is(err, stop) || errors.Is(err, ErrArchive) {
		t.Errorf("ReadArchive() stopped by fn = %v, want fn's error and no ErrArchive", err)
	}

	for _, tt := range []struct {
		name string
		in   []*zip.File
		want string
	}{
		{"a submodule", archive(t, entry{name: "top/"}, entry{name: "top/a.txt", content: "a"},
			entry{name: "top/vendor/lib/"}), "vendor/lib is a submodule"},
		{"a file outside the top directory", archive(t, entry{name: "a.txt", content: "a"}),
			"a.txt is not below its top directory"},
		{"a path git can't hold", archive(t, entry{name: "top/../a.txt", content: "a"}),
			"has a path git can't hold"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ReadArchive(ctx, tt.in, func(ports.SourceFile, io.Reader) error { return nil })
			if !errors.Is(err, ErrArchive) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ReadArchive() = %v, want ErrArchive saying %q", err, tt.want)
			}
		})
	}
}
