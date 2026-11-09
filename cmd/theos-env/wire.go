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

package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sync"
)

// How a run goes over the wire. The request is a POST to /run with the
// lease's token as a bearer token, the command as a JSON array in
// headerCommand, the folder it runs in, relative to the tree's root, in
// headerDir, and the tree as a gzip'd tar. The answer is a stream of
// frames: output as it comes, then the files the command changed or added
// as a gzip'd tar, then the exit code, last.
const (
	headerCommand = "Theos-Command"
	headerDir     = "Theos-Dir"
)

// The kinds of frame.
const (
	frameStdout  = 'o'
	frameStderr  = 'e'
	frameMessage = 'm' // theos-env's own words, shown on the caller's stderr
	frameFiles   = 'f' // a piece of the changed files' tar
	frameExit    = 'x' // the exit code, in decimal: the last frame
)

// maxFrame bounds a frame's payload.
const maxFrame = 1 << 20

// frameWriter writes frames, one at a time, and flushes each.
type frameWriter struct {
	mu    sync.Mutex
	w     io.Writer
	flush func()
}

func (fw *frameWriter) frame(kind byte, payload []byte) error {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	for first := true; first || len(payload) > 0; first = false {
		n := min(len(payload), maxFrame)
		var head [5]byte
		head[0] = kind
		binary.BigEndian.PutUint32(head[1:], uint32(n)) //nolint:gosec // n is at most maxFrame
		if _, err := fw.w.Write(head[:]); err != nil {
			return err
		}
		if _, err := fw.w.Write(payload[:n]); err != nil {
			return err
		}
		payload = payload[n:]
	}
	if fw.flush != nil {
		fw.flush()
	}
	return nil
}

// stream is an io.Writer of frames of one kind.
type stream struct {
	fw   *frameWriter
	kind byte
}

func (s stream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.fw.frame(s.kind, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// readFrame reads the next frame; io.EOF when the stream ended between
// frames.
func readFrame(r io.Reader) (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("a frame of %d bytes, over %d", n, maxFrame)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return head[0], payload, nil
}

// errOverCap is a tree with more bytes of files than the cap.
var errOverCap = errors.New("over the cap")

// writeTree writes the files named, slash paths under root, as a gzip'd
// tar: regular files with their mode, and symbolic links. A name that is
// gone, or a folder (a submodule), is left out.
func writeTree(w io.Writer, root *os.Root, names []string) (err error) {
	zw, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	tw := tar.NewWriter(zw)
	defer func() {
		if cerr := tw.Close(); err == nil {
			err = cerr
		}
		if cerr := zw.Close(); err == nil {
			err = cerr
		}
	}()
	for _, name := range names {
		local := filepath.FromSlash(name)
		info, err := root.Lstat(local)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		switch {
		case info.Mode().IsRegular():
			if err := writeFile(tw, root, name, info); err != nil {
				return err
			}
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := root.Readlink(local)
			if err != nil {
				return err
			}
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target,
				Mode: 0o777, ModTime: info.ModTime()}); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeFile(tw *tar.Writer, root *os.Root, name string, info fs.FileInfo) error {
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: info.Size(),
		Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}); err != nil {
		return err
	}
	_, err = io.CopyN(tw, f, info.Size())
	return err
}

// treeFile is a file readTree wrote: its slash path, and the sha256 of a
// regular file's bytes or a link's target.
type treeFile struct {
	name string
	sum  [sha256.Size]byte
}

// readTree writes a gzip'd tar from r into root, at most max bytes of files
// (errOverCap past them), and returns what it wrote. Only names inside root
// are taken, and os.Root keeps every write in it, through links too. A
// file or link already there is replaced.
func readTree(r io.Reader, root *os.Root, max int64) ([]treeFile, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	var written []treeFile
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return written, nil
		}
		if err != nil {
			return written, err
		}
		name := path.Clean(h.Name)
		local := filepath.FromSlash(name)
		if !filepath.IsLocal(local) {
			return written, fmt.Errorf("the tree names %q, outside it", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeReg:
			if total += h.Size; total > max {
				return written, errOverCap
			}
			sum, err := writeTreeFile(root, local, tr, h)
			if err != nil {
				return written, err
			}
			written = append(written, treeFile{name: name, sum: sum})
		case tar.TypeSymlink:
			if err := makeParent(root, local); err != nil {
				return written, err
			}
			if err := replace(root, local); err != nil {
				return written, err
			}
			if err := root.Symlink(h.Linkname, local); err != nil {
				return written, err
			}
			written = append(written, treeFile{name: name, sum: sha256.Sum256([]byte(h.Linkname))})
		case tar.TypeDir:
			if err := root.MkdirAll(local, 0o755); err != nil {
				return written, err
			}
		}
	}
}

func writeTreeFile(root *os.Root, local string, r io.Reader, h *tar.Header) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	if err := makeParent(root, local); err != nil {
		return sum, err
	}
	if err := replace(root, local); err != nil {
		return sum, err
	}
	mode := fs.FileMode(h.Mode).Perm() | 0o600
	f, err := root.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return sum, err
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(f, hash), r, h.Size); err != nil {
		_ = f.Close()
		return sum, err
	}
	if err := f.Close(); err != nil {
		return sum, err
	}
	// The umask may have taken bits off; the tree's mode stands.
	if err := root.Chmod(local, mode); err != nil {
		return sum, err
	}
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}

// makeParent makes the folders local is in.
func makeParent(root *os.Root, local string) error {
	if dir := filepath.Dir(local); dir != "." {
		return root.MkdirAll(dir, 0o755)
	}
	return nil
}

// replace removes a file or link at local, so a write makes a new one and
// never follows a link; a folder stays.
func replace(root *os.Root, local string) error {
	info, err := root.Lstat(local)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a folder", filepath.ToSlash(local))
	}
	return root.Remove(local)
}
