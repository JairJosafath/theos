package gitea

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// TestGitHostContractIntegration runs the GitHost contract suite on the seeded
// Gitea, each subtest in an organization of its own, like TestGiteaIntegration.
func TestGitHostContractIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	contract.GitHost(t, contract.GitHostEnv{
		New: func(t *testing.T) ports.GitHost {
			owner := fmt.Sprintf("contract-%d", time.Now().UnixNano())
			createOrg(t, base, owner, creds.Token)
			// Cleanups run last first: the repository goes before its organization.
			t.Cleanup(func() { deleteRepo(t, base, owner, "contract", creds.Token) })
			p, err := (Client{}).Provider(ports.Config{URL: base, Owner: owner, Credentials: creds})
			if err != nil {
				t.Fatal(err)
			}
			return p
		},
		SetStatus: func(t *testing.T, repo ports.Repository, commit, context, state string) {
			giteaJSON(t, base, creds.Token, http.MethodPost,
				fmt.Sprintf("/api/v1/repos/%s/%s/statuses/%s", repo.Owner, repo.Name, commit),
				map[string]string{"state": state, "context": context}, nil)
		},
		Release: func(t *testing.T, repo ports.Repository, tag, commit, body, asset, content string) {
			id := createRelease(t, base, creds.Token, repo, tag, commit)
			giteaJSON(t, base, creds.Token, http.MethodPatch,
				fmt.Sprintf("/api/v1/repos/%s/%s/releases/%d", repo.Owner, repo.Name, id),
				map[string]string{"body": body}, nil)
			uploadAsset(t, base, creds.Token, repo, id, asset, content)
		},
	})
}
