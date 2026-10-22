package features

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallVerifiesTheDownload runs the feature's install.sh against a server
// standing in for the UI's /downloads/ or the release's files: the binary it
// downloads is checked against the SHA256SUMS next to it, and a mismatch, a
// missing SHA256SUMS or one that doesn't list the binary leaves the client
// uninstalled, without failing the build (#1175).
func TestInstallVerifiesTheDownload(t *testing.T) {
	for _, tool := range []string{"sh", "sha256sum", "awk"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	binary := "#!/bin/sh\necho fake c4session\n"
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(binary)))
	other := fmt.Sprintf("%x", sha256.Sum256([]byte("another build")))

	cases := []struct {
		name      string
		sums      string // the SHA256SUMS served; empty serves 404
		installed bool
		warning   string
	}{
		{"a matching SHA256SUMS installs", sum + "  c4session-linux-amd64\n" + other + "  c4session-linux-arm64\n", true, ""},
		{"a mismatch is refused", other + "  c4session-linux-amd64\n", false, "checksum mismatch for c4session-linux-amd64"},
		{"a SHA256SUMS without the binary is refused", other + "  c4session-linux-arm64\n", false, "SHA256SUMS doesn't list c4session-linux-amd64"},
		{"no SHA256SUMS is refused", "", false, "no SHA256SUMS next to the binary"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/downloads/c4session-linux-amd64":
					fmt.Fprint(w, binary)
				case "/downloads/SHA256SUMS":
					if c.sums == "" {
						http.NotFound(w, r)
						return
					}
					fmt.Fprint(w, c.sums)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			bin := t.TempDir()
			cmd := exec.Command("sh", filepath.Join("src", "c4session", "install.sh"))
			cmd.Env = append(os.Environ(),
				"BINARYURL="+srv.URL+"/downloads/c4session-linux-amd64",
				"C4SESSION_BIN_DIR="+bin)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install.sh failed the build: %v\n%s", err, out)
			}

			got, readErr := os.ReadFile(filepath.Join(bin, "c4session"))
			switch {
			case c.installed && readErr != nil:
				t.Errorf("c4session was not installed: %v\n%s", readErr, out)
			case c.installed && string(got) != binary:
				t.Errorf("installed c4session = %q, want the download", got)
			case !c.installed && readErr == nil:
				t.Errorf("an unverified c4session was installed\n%s", out)
			}
			if c.installed {
				if !strings.Contains(string(out), "matches SHA256SUMS") {
					t.Errorf("install.sh doesn't say it verified the download:\n%s", out)
				}
			} else if !strings.Contains(string(out), c.warning) || !strings.Contains(string(out), "was not installed") {
				t.Errorf("install.sh output lacks %q and the not-installed warning:\n%s", c.warning, out)
			}
			leftovers, _ := filepath.Glob(filepath.Join(bin, "c4session.*"))
			if len(leftovers) != 0 {
				t.Errorf("install.sh left %v behind", leftovers)
			}
			if _, err := os.Stat(filepath.Join(bin, "c4session-start")); err != nil {
				t.Errorf("c4session-start was not written: %v", err)
			}
		})
	}
}
