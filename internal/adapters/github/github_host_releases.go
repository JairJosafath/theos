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

package github

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/JairJosafath/theos/internal/adapters/git"
	"github.com/JairJosafath/theos/internal/ports"
)

// Releases and a tag's source on GitHub, step 2 of docs/plans/github-host.md
// (#75).

type ghRelease struct {
	ID      int64  `json:"id"`
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (r ghRelease) release() *ports.Release {
	rel := &ports.Release{ID: r.ID, Tag: r.TagName, Name: r.Name, Body: r.Body, Draft: r.Draft, URL: r.HTMLURL}
	for _, a := range r.Assets {
		rel.Assets = append(rel.Assets, ports.ReleaseAsset{ID: a.ID, Name: a.Name, Size: a.Size})
	}
	return rel
}

// GetRelease returns the release of tag with its assets, or nil when repo has
// none. GitHub finds only a published release by its tag, so a draft is
// looked for in the list of releases, which shows drafts to who may write.
func (h *host) GetRelease(ctx context.Context, repo ports.Repository, tag string) (*ports.Release, error) {
	what := "reading the release of " + tag + " in " + repo.Owner + "/" + repo.Name
	status, data, err := h.do(ctx, http.MethodGet, h.repoPath(repo)+"/releases/tags/"+pathEscapeRef(tag), nil)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
		var r ghRelease
		if err := json.Unmarshal(data, &r); err != nil || r.ID == 0 {
			return nil, fmt.Errorf("github: %s: the answer is no release", what)
		}
		return r.release(), nil
	case http.StatusNotFound:
	default:
		return nil, failed(what, status, data)
	}
	raw, err := h.list(ctx, h.repoPath(repo)+"/releases", nil)
	if err != nil {
		return nil, err
	}
	for _, one := range raw {
		var r ghRelease
		if err := json.Unmarshal(one, &r); err != nil {
			return nil, fmt.Errorf("github: %s: %w", what, err)
		}
		if r.Draft && r.TagName == tag {
			return r.release(), nil
		}
	}
	return nil, nil
}

// download starts a GET whose body is streamed: no timeout of one call
// bounds it, ctx does. GitHub answers an asset and an archive with a
// redirect to where the bytes are, which the client follows, leaving the
// token behind when that is another host.
func (h *host) download(ctx context.Context, path, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.api.String()+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.creds.Token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	client := *h.http
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: GET %s: %w", path, err)
	}
	return resp, nil
}

// refused is the error of a download that answered anything but 200; it
// closes the response.
func refused(what string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	return failed(what, resp.StatusCode, data)
}

// OpenReleaseAsset starts the download of asset and returns its body, which
// the caller closes, and its size in bytes. The body is streamed, never held
// whole, and fails as it is read if it is not that size.
func (h *host) OpenReleaseAsset(ctx context.Context, repo ports.Repository, asset ports.ReleaseAsset) (io.ReadCloser, int64, error) {
	name := asset.Name + " of " + repo.Owner + "/" + repo.Name
	resp, err := h.download(ctx, h.repoPath(repo)+"/releases/assets/"+strconv.FormatInt(asset.ID, 10),
		"application/octet-stream")
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, refused("downloading "+name, resp)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != asset.Size {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("github: %s is %d bytes, the release says %d", name, resp.ContentLength, asset.Size)
	}
	return &sizedBody{body: resp.Body, left: asset.Size, name: name}, asset.Size, nil
}

// sizedBody is a download that must be exactly as long as the release says,
// also when the host sends no Content-Length.
type sizedBody struct {
	body io.ReadCloser
	left int64
	name string
}

func (b *sizedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var one [1]byte
		if n, _ := b.body.Read(one[:]); n > 0 {
			return 0, fmt.Errorf("github: %s is longer than the release says", b.name)
		}
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.body.Read(p)
	b.left -= int64(n)
	if errors.Is(err, io.EOF) {
		if b.left > 0 {
			return n, fmt.Errorf("github: %s ended %d bytes short: %w", b.name, b.left, io.ErrUnexpectedEOF)
		}
		err = nil
	}
	return n, err
}

func (b *sizedBody) Close() error { return b.body.Close() }

// SetReleaseBody replaces the notes of release id with body. GitHub would
// empty them for a body of "", so that call is not made.
func (h *host) SetReleaseBody(ctx context.Context, repo ports.Repository, id int64, body string) error {
	if body == "" {
		return nil
	}
	status, data, err := h.do(ctx, http.MethodPatch, h.repoPath(repo)+"/releases/"+strconv.FormatInt(id, 10),
		map[string]string{"body": body})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return failed("setting the notes of release "+strconv.FormatInt(id, 10)+" of "+repo.Owner+"/"+repo.Name,
			status, data)
	}
	return nil
}

// maxSourceArchive bounds the archive ReadSource downloads. The archive goes
// to the operator's /tmp, a volume of 1 GiB; past that the pod is evicted
// rather than the read failing.
const maxSourceArchive = 256 << 20

// ReadSource reads the files of repo as of tag from GitHub's zip archive of
// the tag, downloaded to a temporary file, and calls fn with each file and
// its content, in the archive's order (git.ReadArchive). A submodule fails
// the read. An error from fn stops the read and is returned.
func (h *host) ReadSource(ctx context.Context, repo ports.Repository, tag string,
	fn func(ports.SourceFile, io.Reader) error) error {
	what := "the archive of " + repo.Owner + "/" + repo.Name + " at " + tag
	resp, err := h.download(ctx, h.repoPath(repo)+"/zipball/"+pathEscapeRef("refs/tags/"+tag),
		"application/vnd.github+json")
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return refused("downloading "+what, resp)
	}
	defer func() { _ = resp.Body.Close() }()
	archive, err := os.CreateTemp("", "theos-source-*.zip")
	if err != nil {
		return err
	}
	defer func() {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
	}()
	size, err := io.Copy(archive, io.LimitReader(resp.Body, maxSourceArchive+1))
	if err != nil {
		return fmt.Errorf("github: downloading %s: %w", what, err)
	}
	if size > maxSourceArchive {
		return fmt.Errorf("github: %s is larger than %d MiB", what, maxSourceArchive>>20)
	}
	files, err := zip.NewReader(archive, size)
	if err != nil {
		return fmt.Errorf("github: reading %s: %w", what, err)
	}
	if err := git.ReadArchive(ctx, files.File, fn); errors.Is(err, git.ErrArchive) {
		return fmt.Errorf("github: %s: %w", what, err)
	} else if err != nil {
		return err
	}
	return nil
}
