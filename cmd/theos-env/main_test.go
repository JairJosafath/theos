package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

// gitRepo makes a repository with a tracked a.txt and sub/b.txt, an
// untracked untracked.txt, and an ignored secret.env, and returns its
// folder.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"a.txt": "first\n", "sub/b.txt": "b\n", ".gitignore": "node_modules/\nsecret.env\n",
		"secret.env": "KEY=not-for-the-environment\n", "untracked.txt": "u\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "a.txt", "sub/b.txt", ".gitignore"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// environment serves runs as a lease pod does, on a work folder of its
// own, and returns its URL, token and work folder.
func environment(t *testing.T) (url, token, work string) {
	t.Helper()
	token, work = rand.Text(), t.TempDir()
	srv := httptest.NewServer(&server{token: token, work: work, max: model.TestEnvMaxTree})
	t.Cleanup(srv.Close)
	return srv.URL, token, work
}

// runIn runs theos-env run browser -- sh -c script in folder, with the
// lease's variables, and returns its exit code and output.
func runIn(t *testing.T, folder, url, token, script string) (int, string, string) {
	t.Helper()
	t.Chdir(folder)
	vars := map[string]string{"THEOS_ENV_BROWSER_URL": url, "THEOS_ENV_BROWSER_TOKEN": token}
	var stdout, stderr bytes.Buffer
	code := mainWith([]string{"run", "browser", "--", "sh", "-c", script}, &stdout, &stderr,
		func(k string) string { return vars[k] })
	return code, stdout.String(), stderr.String()
}

// A run sends the tracked and untracked, not ignored, files, runs the
// command in the folder it was called from, streams its output, brings
// back the file it changed and the one it added, the ignored ones it made
// left out, and passes its exit code (T4).
func TestRunSendsTheTreeAndBringsBackChanges(t *testing.T) {
	repo := gitRepo(t)
	url, token, _ := environment(t)
	// serve's own token never reaches the command.
	t.Setenv(envToken, token)
	code, stdout, stderr := runIn(t, filepath.Join(repo, "sub"), url, token, `
test -f ../untracked.txt && echo untracked-sent
test -e ../secret.env && echo ignored-sent
test -e ../.git && echo git-sent
echo "in $(basename "$PWD"), token=$THEOS_ENV_TOKEN"
cat ../a.txt
echo again > ../a.txt
mkdir -p ../pics && echo png > ../pics/new.png
mkdir -p ../node_modules && echo dep > ../node_modules/dep.js
echo oops >&2
exit 3`)
	if code != 3 {
		t.Errorf("exit code %d, want the command's 3; stderr: %s", code, stderr)
	}
	for _, want := range []string{"untracked-sent", "in sub, token=\n", "first"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q lacks %q", stdout, want)
		}
	}
	for _, unwanted := range []string{"ignored-sent", "git-sent"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout %q says %s", stdout, unwanted)
		}
	}
	for _, want := range []string{"oops", "a.txt", "pics/new.png"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q lacks %q: the command's and the files brought back", stderr, want)
		}
	}
	for name, want := range map[string]string{"a.txt": "again\n", "pics/new.png": "png\n", "sub/b.txt": "b\n"} {
		if got, err := os.ReadFile(filepath.Join(repo, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "node_modules")); err == nil {
		t.Error("an ignored folder the command made came back")
	}
}

// Each run gets the tree it sends, nothing a run before left in /work.
func TestRunStartsOnAFreshTree(t *testing.T) {
	repo := gitRepo(t)
	url, token, _ := environment(t)
	first, _, stderr := runIn(t, repo, url, token, "mkdir -p node_modules && echo dep > node_modules/dep.js")
	if first != 0 {
		t.Fatalf("first run: exit %d: %s", first, stderr)
	}
	code, stdout, stderr := runIn(t, repo, url, token, "test -e node_modules/dep.js && echo leftover; true")
	if code != 0 || strings.Contains(stdout, "leftover") {
		t.Errorf("second run: exit %d, stdout %q, stderr %q; want no leftover", code, stdout, stderr)
	}
}

// The environment takes runs with its lease's token alone (T5).
func TestRunRefusesAWrongToken(t *testing.T) {
	repo := gitRepo(t)
	url, _, work := environment(t)
	code, stdout, stderr := runIn(t, repo, url, rand.Text(), "echo ran > ran.txt")
	if code != exitFailed || stdout != "" || !strings.Contains(stderr, "401") {
		t.Errorf("exit %d, stdout %q, stderr %q; want refused with 401", code, stdout, stderr)
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("the environment took a tree from a wrong token: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(repo, "ran.txt")); err == nil {
		t.Error("a refused run changed the tree")
	}
}

// A remote environment serves HTTPS with the certificate its lease's token
// makes, at an address the certificate doesn't name; a run that pins that
// certificate (THEOS_ENV_<NAME>_CA) reaches it, and one that pins another,
// or none, sends it nothing (T4, T5).
func TestRunPinsARemoteEnvironmentsCertificate(t *testing.T) {
	repo := gitRepo(t)
	token, work := rand.Text(), t.TempDir()
	config, err := serveTLS(token)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(&server{token: token, work: work, max: model.TestEnvMaxTree})
	srv.TLS = config
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pinned, _, err := model.TestEnvCertificate(token)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := model.TestEnvCertificate(rand.Text())
	if err != nil {
		t.Fatal(err)
	}
	run := func(ca string) (int, string, string) {
		t.Chdir(repo)
		vars := map[string]string{"THEOS_ENV_GPU_URL": srv.URL, "THEOS_ENV_GPU_TOKEN": token, "THEOS_ENV_GPU_CA": ca}
		var stdout, stderr bytes.Buffer
		code := mainWith([]string{"run", "gpu", "--", "sh", "-c", "cat a.txt; echo made > made.txt"}, &stdout, &stderr,
			func(k string) string { return vars[k] })
		return code, stdout.String(), stderr.String()
	}
	if code, stdout, stderr := run(pinned); code != 0 || stdout != "first\n" {
		t.Fatalf("a run pinning the lease's certificate: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "made.txt")); err != nil || string(got) != "made\n" {
		t.Errorf("made.txt = %q, %v; want the file the command made", got, err)
	}
	if err := os.Remove(filepath.Join(repo, "made.txt")); err != nil {
		t.Fatal(err)
	}
	for name, ca := range map[string]string{"another certificate": other, "none": ""} {
		code, stdout, stderr := run(ca)
		if code != exitFailed || stdout != "" || !strings.Contains(stderr, "certificate") {
			t.Errorf("a run pinning %s: exit %d, stdout %q, stderr %q; want it refused for the certificate", name, code,
				stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "made.txt")); err == nil {
		t.Error("a run that pinned the wrong certificate ran")
	}
}

// A tree over the cap is refused before it is sent, and the environment
// refuses one too (T4).
func TestRunRefusesATreeOverTheCap(t *testing.T) {
	repo := gitRepo(t)
	url, token, _ := environment(t)
	t.Chdir(repo)
	var stdout, stderr bytes.Buffer
	c := caller{name: "browser", url: url, token: token, max: 4, stdout: &stdout, stderr: &stderr}
	if _, err := c.run(context.Background(), []string{"true"}); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Errorf("run of a tree over the cap = %v, want it refused", err)
	}

	srv := httptest.NewServer(&server{token: token, work: t.TempDir(), max: 4})
	defer srv.Close()
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "big.bin", Size: 5, Mode: 0o644})
	_, _ = tw.Write([]byte("12345"))
	_ = tw.Close()
	_ = zw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/run", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerCommand, `["true"]`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("the environment answered a tree over its cap %s, want 413", resp.Status)
	}
}

// A tree that names a path outside it is refused, and nothing is written.
func TestServeRefusesAPathOutsideTheTree(t *testing.T) {
	token, parent := rand.Text(), t.TempDir()
	work := filepath.Join(parent, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(&server{token: token, work: work, max: model.TestEnvMaxTree})
	defer srv.Close()
	var body bytes.Buffer
	zw := gzip.NewWriter(&body)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "../escaped.txt", Size: 1, Mode: 0o644})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = zw.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/run", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerCommand, `["true"]`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %s, want 400", resp.Status)
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); err == nil {
		t.Error("the tree wrote outside the work folder")
	}
}

// A run with no lease for the environment says which variables it lacks.
func TestRunWithoutALease(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := mainWith([]string{"run", "android", "--", "true"}, &stdout, &stderr, func(string) string { return "" })
	if code != exitFailed || !strings.Contains(stderr.String(), "THEOS_ENV_ANDROID_URL") {
		t.Errorf("exit %d, stderr %q; want it to name THEOS_ENV_ANDROID_URL", code, stderr.String())
	}
}

// serve answers its readiness probe without a token, and won't start
// without one.
func TestServeHealthAndToken(t *testing.T) {
	srv := httptest.NewServer(&server{token: rand.Text(), work: t.TempDir()})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz %s, want 200", resp.Status)
	}
	var stderr bytes.Buffer
	if code := mainWith([]string{"serve"}, &stderr, &stderr, func(string) string { return "" }); code != exitFailed ||
		!strings.Contains(stderr.String(), envToken) {
		t.Errorf("serve without a token: exit %d, %q", code, stderr.String())
	}
}

// install copies the binary, executable, where the lease pod's init
// container says.
func TestInstall(t *testing.T) {
	to := filepath.Join(t.TempDir(), "bin", "theos-env")
	var stderr bytes.Buffer
	if code := mainWith([]string{"install", to}, &stderr, &stderr, os.Getenv); code != 0 {
		t.Fatalf("install: exit %d: %s", code, stderr.String())
	}
	info, err := os.Stat(to)
	if err != nil || info.Mode().Perm()&0o111 == 0 || info.Size() == 0 {
		t.Errorf("installed %v, %v; want an executable copy", info, err)
	}
}
