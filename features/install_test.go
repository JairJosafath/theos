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

package features

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installTools are the commands install.sh runs. The test gives it a PATH of
// these alone, so a cosign installed on the machine running the test never
// stands in for the fake one, or for none.
var installTools = []string{"sh", "curl", "sha256sum", "awk", "sed", "cut", "chmod", "mv", "rm", "cat", "uname"}

// fakeCosign stands in for cosign verify-blob: it records its arguments and
// accepts the bundle whose content is "good", as a signature that verifies.
const fakeCosign = `#!/bin/sh
printf '%s\n' "$@" > "$COSIGN_ARGS"
while [ $# -gt 0 ]; do
    if [ "$1" = --bundle ]; then bundle=$2; fi
    shift
done
if [ "$(cat "$bundle")" = good ]; then
    echo "Verified OK"
    exit 0
fi
echo "Error: none of the expected identities matched what was in the certificate" >&2
exit 1
`

// TestInstallVerifiesTheDownload runs the feature's install.sh against a server
// standing in for the UI's /downloads/ or the release's files: the binary it
// downloads is checked against the SHA256SUMS next to it, and a mismatch, a
// missing SHA256SUMS or one that doesn't list the binary leaves the client
// uninstalled, without failing the build (#1175). With cosign on PATH,
// SHA256SUMS's keyless signature (SHA256SUMS.sigstore.json) must verify as the
// Release workflow's, or the download is refused too; without cosign, or
// without a signature at the origin, a notice says it wasn't checked (#1706).
func TestInstallVerifiesTheDownload(t *testing.T) {
	tools := t.TempDir()
	for _, tool := range installTools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not installed", tool)
		}
		if err := os.Symlink(p, filepath.Join(tools, tool)); err != nil {
			t.Fatal(err)
		}
	}
	identity, issuer := signerDefaults(t)

	binary := "#!/bin/sh\necho fake theos-session\n"
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(binary)))
	other := fmt.Sprintf("%x", sha256.Sum256([]byte("another build")))
	good := sum + "  theos-session-linux-amd64\n" + other + "  theos-session-linux-arm64\n"

	cases := []struct {
		name      string
		sums      string // the SHA256SUMS served; empty serves 404
		bundle    string // the SHA256SUMS.sigstore.json served; empty serves 404
		cosign    bool   // a cosign on PATH
		installed bool
		says      string // in the output: the notice, or the warning that refused it
	}{
		{"a matching SHA256SUMS installs", good, "", false, true, "cosign is not on PATH"},
		{"a mismatch is refused", other + "  theos-session-linux-amd64\n", "", false, false,
			"checksum mismatch for theos-session-linux-amd64"},
		{"a SHA256SUMS without the binary is refused", other + "  theos-session-linux-arm64\n", "", false, false,
			"SHA256SUMS doesn't list theos-session-linux-amd64"},
		{"no SHA256SUMS is refused", "", "", false, false, "no SHA256SUMS next to the binary"},
		{"a good signature installs (#1706)", good, "good", true, true, "signature verifies as the release's"},
		{"a bad signature is refused (#1706)", good, "forged", true, false, "signature doesn't verify"},
		{"no signature at the origin is a notice (#1706)", good, "", true, true,
			"no SHA256SUMS.sigstore.json next to SHA256SUMS"},
		{"without cosign a signature is not checked (#1706)", good, "forged", false, true, "cosign is not on PATH"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serve := map[string]string{
					"/downloads/theos-session-linux-amd64": binary,
					"/downloads/SHA256SUMS":                c.sums,
					"/downloads/SHA256SUMS.sigstore.json":  c.bundle,
				}[r.URL.Path]
				if serve == "" {
					http.NotFound(w, r)
					return
				}
				_, _ = fmt.Fprint(w, serve)
			}))
			defer srv.Close()

			path := tools
			args := filepath.Join(t.TempDir(), "cosign-args")
			if c.cosign {
				fake := t.TempDir()
				if err := os.WriteFile(filepath.Join(fake, "cosign"), []byte(fakeCosign), 0o755); err != nil {
					t.Fatal(err)
				}
				path = fake + string(os.PathListSeparator) + tools
			}
			bin := t.TempDir()
			cmd := exec.Command(filepath.Join(tools, "sh"), filepath.Join("src", "theos-session", "install.sh"))
			cmd.Env = []string{"PATH=" + path, "HOME=" + t.TempDir(), "COSIGN_ARGS=" + args,
				"BINARYURL=" + srv.URL + "/downloads/theos-session-linux-amd64",
				"THEOS_SESSION_BIN_DIR=" + bin}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("install.sh failed the build: %v\n%s", err, out)
			}

			got, readErr := os.ReadFile(filepath.Join(bin, "theos-session"))
			switch {
			case c.installed && readErr != nil:
				t.Errorf("theos-session was not installed: %v\n%s", readErr, out)
			case c.installed && string(got) != binary:
				t.Errorf("installed theos-session = %q, want the download", got)
			case !c.installed && readErr == nil:
				t.Errorf("an unverified theos-session was installed\n%s", out)
			}
			if !strings.Contains(string(out), c.says) {
				t.Errorf("install.sh output lacks %q:\n%s", c.says, out)
			}
			if c.installed && !strings.Contains(string(out), "matches SHA256SUMS") {
				t.Errorf("install.sh doesn't say it verified the download:\n%s", out)
			}
			if !c.installed && !strings.Contains(string(out), "was not installed") {
				t.Errorf("install.sh output lacks the not-installed warning:\n%s", out)
			}
			if c.cosign && c.bundle != "" {
				// cosign checks the downloaded SHA256SUMS against the bundle,
				// as the feature's default signer: the Release workflow on a
				// version tag, through GitHub Actions' OIDC issuer.
				recorded, err := os.ReadFile(args)
				if err != nil {
					t.Fatalf("cosign was not run: %v\n%s", err, out)
				}
				a := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
				want := []string{"verify-blob", "--bundle", filepath.Join(bin, "theos-session.bundle"),
					"--certificate-identity-regexp", identity, "--certificate-oidc-issuer", issuer,
					filepath.Join(bin, "theos-session.sums")}
				if strings.Join(a, "\n") != strings.Join(want, "\n") {
					t.Errorf("cosign ran with %q, want %q", a, want)
				}
			}
			leftovers, _ := filepath.Glob(filepath.Join(bin, "theos-session.*"))
			if len(leftovers) != 0 {
				t.Errorf("install.sh left %v behind", leftovers)
			}
			if _, err := os.Stat(filepath.Join(bin, "theos-session-start")); err != nil {
				t.Errorf("theos-session-start was not written: %v", err)
			}
		})
	}
}

// signerDefaults are the defaults of the feature's signerIdentity and
// signerIssuer options, which install.sh must fall back to when the
// devcontainer sets neither.
func signerDefaults(t *testing.T) (identity, issuer string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("src", "theos-session", "devcontainer-feature.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Options map[string]struct {
			Default string `json:"default"`
		} `json:"options"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	identity, issuer = meta.Options["signerIdentity"].Default, meta.Options["signerIssuer"].Default
	if identity == "" || issuer == "" {
		t.Fatalf("the feature has no default signerIdentity (%q) or signerIssuer (%q)", identity, issuer)
	}
	return identity, issuer
}
