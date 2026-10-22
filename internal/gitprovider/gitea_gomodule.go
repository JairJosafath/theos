package gitprovider

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

// PublishGoModule publishes the Go module at the root of repo, as of tag, to
// the Gitea Go package registry of repo's owner, under the module path its
// go.mod declares and with tag as its version. A repository with no go.mod, a
// version the registry has already and a module that can't be published at
// that version are outcomes, not errors; a repeat publishes nothing.
//
// The module zip is built from the tag's archive with golang.org/x/mod/zip, as
// the go command would: vendored packages and nested modules are left out.
func (g *gitea) PublishGoModule(ctx context.Context, repo Repository, tag string) (GoModule, error) {
	out := GoModule{Version: tag}
	goMod, ok, err := g.rawFile(ctx, repo, tag, "go.mod")
	if err != nil {
		return out, err
	}
	if !ok {
		out.Outcome = GoModuleNoGoMod
		return out, nil
	}
	out.Path = modfile.ModulePath(goMod)
	if reason := checkModuleVersion(out.Path, tag); reason != "" {
		out.Outcome, out.Reason = GoModuleInvalid, reason
		return out, nil
	}
	mod := module.Version{Path: out.Path, Version: tag}

	exists, err := g.goModuleExists(ctx, repo.Owner, mod)
	if err != nil {
		return out, err
	}
	if exists {
		out.Outcome = GoModuleExists
		return out, nil
	}

	// An archive larger than a module zip may be can't become one.
	archive, err := g.downloadArchive(ctx, repo, tag, modzip.MaxZipFile)
	if err != nil {
		return out, err
	}
	defer removeTemp(archive)
	info, err := archive.Stat()
	if err != nil {
		return out, err
	}
	files, err := zip.NewReader(archive, info.Size())
	if err != nil {
		return out, fmt.Errorf("gitea: reading the archive of %s at %s: %w", repoPath(repo), tag, err)
	}
	modZip, err := os.CreateTemp("", "c4-module-*.zip")
	if err != nil {
		return out, err
	}
	defer removeTemp(modZip)
	written := &trackedWriter{w: modZip}
	if err := repackModule(written, files, mod); err != nil {
		if written.err != nil {
			return out, fmt.Errorf("gitea: writing the module zip of %s@%s: %w", mod.Path, mod.Version, written.err)
		}
		out.Outcome, out.Reason = GoModuleInvalid, err.Error()
		return out, nil
	}
	out.Outcome, err = g.uploadGoModule(ctx, repo.Owner, mod, modZip)
	return out, err
}

// trackedWriter keeps the first error of its writer, so a failed write, such
// as a full disk, isn't taken for a module Go refuses.
type trackedWriter struct {
	w   io.Writer
	err error
}

func (t *trackedWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if err != nil && t.err == nil {
		t.err = err
	}
	return n, err
}

// removeTemp closes and removes a temporary file.
func removeTemp(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}

// checkModuleVersion says why path can't be published at version, or "" when
// it can.
func checkModuleVersion(path, version string) string {
	if path == "" {
		return "go.mod declares no module path"
	}
	if canonical := module.CanonicalVersion(version); canonical != version {
		return fmt.Sprintf("tag %s is not a canonical semantic version", version)
	}
	if err := module.Check(path, version); err != nil {
		return err.Error()
	}
	return ""
}

// rawFile reads file at ref in repo; ok is false when the repository has no
// such file there.
func (g *gitea) rawFile(ctx context.Context, repo Repository, ref, file string) ([]byte, bool, error) {
	path := "/api/v1/repos/" + repoPath(repo) + "/raw/" + file + "?ref=" + url.QueryEscape(ref)
	status, data, err := g.do(ctx, http.MethodGet, path, nil)
	switch {
	case err != nil:
		return nil, false, err
	case status == http.StatusNotFound:
		return nil, false, nil
	case status != http.StatusOK:
		return nil, false, fmt.Errorf("gitea: reading %s at %s of %s failed (HTTP %d): %s", file, ref, repoPath(repo),
			status, apiMessage(data))
	}
	return data, true, nil
}

// goModulePath is the path of mod's version in owner's Go registry, below
// suffix, such as ".info".
func goModulePath(owner string, mod module.Version, suffix string) (string, error) {
	escPath, err := module.EscapePath(mod.Path)
	if err != nil {
		return "", err
	}
	escVersion, err := module.EscapeVersion(mod.Version)
	if err != nil {
		return "", err
	}
	return "/api/packages/" + url.PathEscape(owner) + "/go/" + escPath + "/@v/" + escVersion + suffix, nil
}

// goModuleExists reports whether owner's Go registry has mod's version.
func (g *gitea) goModuleExists(ctx context.Context, owner string, mod module.Version) (bool, error) {
	path, err := goModulePath(owner, mod, ".info")
	if err != nil {
		return false, err
	}
	status, data, err := g.do(ctx, http.MethodGet, path, nil)
	switch {
	case err != nil:
		return false, err
	case status == http.StatusOK:
		return true, nil
	case status == http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("gitea: reading %s@%s in the Go registry of %s failed (HTTP %d): %s", mod.Path,
		mod.Version, owner, status, apiMessage(data))
}

// uploadGoModule uploads modZip, a module zip written to a file, to owner's Go
// registry. A version the registry has already is GoModuleExists: a version,
// once there, never changes.
func (g *gitea) uploadGoModule(ctx context.Context, owner string, mod module.Version, modZip *os.File) (GoModuleOutcome, error) {
	size, err := modZip.Seek(0, io.SeekEnd)
	if err == nil {
		_, err = modZip.Seek(0, io.SeekStart)
	}
	if err != nil {
		return "", err
	}
	req, err := g.request(ctx, http.MethodPut, "/api/packages/"+url.PathEscape(owner)+"/go/upload", io.NopCloser(modZip))
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/zip")
	resp, err := g.uploadClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("gitea: uploading %s@%s: %w", mod.Path, mod.Version, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	switch resp.StatusCode {
	case http.StatusCreated:
		return GoModulePublished, nil
	case http.StatusConflict:
		return GoModuleExists, nil
	}
	return "", fmt.Errorf("gitea: uploading %s@%s to the Go registry of %s failed (HTTP %d): %s", mod.Path,
		mod.Version, owner, resp.StatusCode, apiMessage(data))
}

// uploadClient is the provider's HTTP client without its timeout for one call:
// ctx bounds a download or an upload, which may take longer.
func (g *gitea) uploadClient() *http.Client {
	client := *g.http
	client.Timeout = 0
	return &client
}

// downloadArchive saves the zip archive of repo at tag to a temporary file and
// returns it, open; the caller closes and removes it. An archive larger than
// limit bytes fails.
func (g *gitea) downloadArchive(ctx context.Context, repo Repository, tag string, limit int64) (*os.File, error) {
	req, err := g.request(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/archive/"+url.PathEscape(tag)+".zip", nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.uploadClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea: downloading the archive of %s at %s: %w", repoPath(repo), tag, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("gitea: downloading the archive of %s at %s failed (HTTP %d): %s", repoPath(repo), tag,
			resp.StatusCode, apiMessage(data))
	}
	f, err := os.CreateTemp("", "c4-archive-*.zip")
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("gitea: the archive of %s at %s is larger than %d bytes", repoPath(repo), tag, limit)
	}
	if err != nil {
		removeTemp(f)
		return nil, err
	}
	return f, nil
}

// repackModule writes the module zip of mod to w from archive, a repository's
// zip archive whose files all sit below one top directory, as Gitea and GitHub
// make them. It fails when the files can't make a module zip, such as a file
// name Go doesn't allow.
func repackModule(w io.Writer, archive *zip.Reader, mod module.Version) error {
	var files []modzip.File
	for _, f := range archive.File {
		if f.FileInfo().IsDir() {
			continue
		}
		_, name, ok := strings.Cut(f.Name, "/")
		if !ok || name == "" {
			return fmt.Errorf("the archive's file %s is not below its top directory", f.Name)
		}
		files = append(files, archiveFile{name: name, f: f})
	}
	return modzip.Create(w, mod, files)
}

// archiveFile is a file of a repository archive, by its path in the repository.
type archiveFile struct {
	name string
	f    *zip.File
}

func (a archiveFile) Path() string                 { return a.name }
func (a archiveFile) Lstat() (os.FileInfo, error)  { return a.f.FileInfo(), nil }
func (a archiveFile) Open() (io.ReadCloser, error) { return a.f.Open() }
