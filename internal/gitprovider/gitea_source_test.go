package gitprovider

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// sourceEntry is a file of a test archive, with the mode its zip header says.
type sourceEntry struct {
	name    string
	content string
	// attrs is the zip header's external attributes, as git archive writes
	// them: an executable's or a symlink's Unix mode in the high 16 bits, made
	// on Unix; 0 for a plain file and 0x10 for a folder, made on FAT.
	attrs uint32
}

// sourceZip builds a repository archive as git archive, and so Gitea, makes
// it: a plain file and a folder made on FAT, an executable and a symlink made
// on Unix with their modes.
func sourceZip(t *testing.T, entries []sourceEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		var creator uint16 // FAT
		if e.attrs>>16 != 0 {
			creator = 3 << 8 // Unix
		}
		f, err := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate, CreatorVersion: creator,
			ExternalAttrs: e.attrs})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(f, e.content)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// archiveHost serves archive as cel-lens/cel-lens-core's archive at any tag.
func archiveHost(t *testing.T, archive []byte) Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/cel-lens/cel-lens-core/archive/v0.1.0.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "cel-lens"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// readFile is a file ReadSource handed on, with its content.
type readFile struct {
	SourceFile
	Content string
}

func readAll(ctx context.Context, p Provider, tag string) ([]readFile, error) {
	var got []readFile
	err := p.ReadSource(ctx, p.Repository("cel-lens-core"), tag, func(f SourceFile, r io.Reader) error {
		data, err := io.ReadAll(r)
		got = append(got, readFile{f, string(data)})
		return err
	})
	return got, err
}

func TestReadSource(t *testing.T) {
	ctx := context.Background()
	const (
		dir        = 0x10
		executable = 0o100755 << 16
		symlink    = 0o120777 << 16
	)

	t.Run("each file with its mode and content", func(t *testing.T) {
		p := archiveHost(t, sourceZip(t, []sourceEntry{
			{name: "cel-lens-core-v0.1.0/", attrs: dir},
			{name: "cel-lens-core-v0.1.0/README.md", content: "# Core\n"},
			{name: "cel-lens-core-v0.1.0/bin/", attrs: dir},
			{name: "cel-lens-core-v0.1.0/bin/run.sh", content: "#!/bin/sh\n", attrs: executable},
			{name: "cel-lens-core-v0.1.0/docs/README.md", content: "../README.md", attrs: symlink},
			{name: "cel-lens-core-v0.1.0/empty"},
		}))
		got, err := readAll(ctx, p, "v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		want := []readFile{
			{SourceFile{Path: "README.md", Mode: SourcePlain, Size: 7}, "# Core\n"},
			{SourceFile{Path: "bin/run.sh", Mode: SourceExecutable, Size: 10}, "#!/bin/sh\n"},
			{SourceFile{Path: "docs/README.md", Mode: SourceSymlink, Size: 12}, "../README.md"},
			{SourceFile{Path: "empty", Mode: SourcePlain}, ""},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ReadSource() read\n%+v\nwant\n%+v", got, want)
		}
	})

	t.Run("a submodule fails the read", func(t *testing.T) {
		p := archiveHost(t, sourceZip(t, []sourceEntry{
			{name: "core/", attrs: dir},
			{name: "core/a"},
			{name: "core/lib/", attrs: dir},
			{name: "core/lib/vendor/", attrs: dir},
		}))
		calls := 0
		err := p.ReadSource(ctx, p.Repository("cel-lens-core"), "v0.1.0", func(SourceFile, io.Reader) error {
			calls++
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "lib/vendor is a submodule") || calls != 0 {
			t.Errorf("ReadSource() = %v after %d calls, want lib/vendor named a submodule before any file", err, calls)
		}
	})

	t.Run("an error from fn stops the read", func(t *testing.T) {
		p := archiveHost(t, sourceZip(t, []sourceEntry{
			{name: "core/a"}, {name: "core/b"},
		}))
		stop := errors.New("stop")
		calls := 0
		err := p.ReadSource(ctx, p.Repository("cel-lens-core"), "v0.1.0", func(SourceFile, io.Reader) error {
			calls++
			return stop
		})
		if !errors.Is(err, stop) || calls != 1 {
			t.Errorf("ReadSource() = %v after %d calls, want %v after 1", err, calls, stop)
		}
	})

	for _, tt := range []struct {
		name  string
		entry sourceEntry
		want  string
	}{
		{"a file outside the top directory", sourceEntry{name: "README.md"}, "not below its top directory"},
		{"a path climbing out", sourceEntry{name: "core/../../etc/passwd"}, "a path git can't hold"},
		{"a path with an empty segment", sourceEntry{name: "core/a//b"}, "a path git can't hold"},
		{"a device", sourceEntry{name: "core/dev", attrs: 0o020644 << 16}, "neither a file nor a symlink"},
	} {
		t.Run(tt.name+" fails the read", func(t *testing.T) {
			p := archiveHost(t, sourceZip(t, []sourceEntry{tt.entry}))
			_, err := readAll(ctx, p, "v0.1.0")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ReadSource() = %v, want an error saying %q", err, tt.want)
			}
		})
	}

	t.Run("a content that isn't what the header says fails as it is read", func(t *testing.T) {
		archive := sourceZip(t, []sourceEntry{{name: "core/a", content: "hello"}})
		// Flip a byte of the deflated content, so its checksum no longer matches.
		r, _ := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		off, _ := r.File[0].DataOffset()
		archive[off] ^= 0xff
		_, err := readAll(ctx, archiveHost(t, archive), "v0.1.0")
		if err == nil {
			t.Error("ReadSource() read a corrupt file")
		}
	})

	t.Run("a tag Gitea lacks is an error", func(t *testing.T) {
		p := archiveHost(t, sourceZip(t, nil))
		if _, err := readAll(ctx, p, "v9.9.9"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("ReadSource() = %v, want the 404", err)
		}
	})
}
