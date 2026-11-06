package gitea

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// TestParseEvent reads a delivery of each kind Gitea 1.27 sent the
// integration test's hook (testdata/events), signed here with a fresh
// secret, and refuses one checked with another secret, unsigned, signed
// with what is not hex, or checked with no secret at all.
func TestParseEvent(t *testing.T) {
	const repo = "events"
	want := map[string]ports.Event{
		"issues":                       {Repository: repo, Number: 1},
		"issue_comment":                {Repository: repo, Number: 1},
		"issue_label":                  {Repository: repo, Number: 1},
		"issue_assign":                 {Repository: repo, Number: 1},
		"create":                       {Repository: repo, Branch: "feature"},
		"push":                         {Repository: repo, Branch: "feature"},
		"pull_request":                 {Repository: repo, Number: 2},
		"pull_request_sync":            {Repository: repo, Number: 2},
		"pull_request_label":           {Repository: repo, Number: 2},
		"pull_request_comment":         {Repository: repo, Number: 2},
		"pull_request_review_approved": {Repository: repo, Number: 2},
		"pull_request_review_rejected": {Repository: repo, Number: 2},
		"pull_request_review_comment":  {Repository: repo, Number: 2},
		"status":                       {Repository: repo},
		"release":                      {Repository: repo},
		"workflow_run":                 {Repository: repo, Branch: ports.DefaultBranch},
	}
	secret := []byte(rand.Text())
	g := &gitea{}
	for kind, w := range want {
		t.Run(kind, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("testdata", "events", kind+".json"))
			if err != nil {
				t.Fatal(err)
			}
			name, err := os.ReadFile(filepath.Join("testdata", "events", kind+".event"))
			if err != nil {
				t.Fatal(err)
			}
			w.Kind = strings.TrimSpace(string(name))
			headers := func(signature string) func(string) string {
				return func(key string) string {
					return map[string]string{"X-Gitea-Event": w.Kind, "X-Gitea-Signature": signature}[key]
				}
			}

			got, err := g.ParseEvent(headers(sign(body, secret)), body, secret)
			if err != nil || got != w {
				t.Errorf("ParseEvent = %+v, %v; want %+v", got, err, w)
			}
			for what, signature := range map[string]string{
				"another secret": sign(body, []byte(rand.Text())),
				"no signature":   "",
				"not hex":        "not-hex",
			} {
				if _, err := g.ParseEvent(headers(signature), body, secret); !errors.Is(err, ports.ErrBadSignature) {
					t.Errorf("with %s ParseEvent gave %v, want ErrBadSignature", what, err)
				}
			}
			if _, err := g.ParseEvent(headers(sign(body, nil)), body, nil); !errors.Is(err, ports.ErrBadSignature) {
				t.Errorf("with no secret ParseEvent gave %v, want ErrBadSignature", err)
			}
		})
	}
}

// sign is the X-Gitea-Signature of body under secret.
func sign(body, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
