package fake_test

import (
	"fmt"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

func TestGitHostContract(t *testing.T) {
	g := fake.New()
	owners := 0
	// statuses are the states SetStatus reported, by owner/name@commit and
	// context.
	statuses := map[string]map[string]string{}
	contract.GitHost(t, contract.GitHostEnv{
		New: func(*testing.T) ports.GitHost {
			owners++
			return g.For(ports.Config{URL: "http://gitea.test", Owner: fmt.Sprintf("contract-%d", owners)})
		},
		SetStatus: func(_ *testing.T, repo ports.Repository, commit, context, state string) {
			key := repo.Owner + "/" + repo.Name + "@" + commit
			if statuses[key] == nil {
				statuses[key] = map[string]string{}
			}
			statuses[key][context] = state
			g.SetCheckContexts(repo.Owner, repo.Name, commit, combined(statuses[key]), statuses[key])
		},
		Release: func(_ *testing.T, repo ports.Repository, tag, commit, body, asset, content string) {
			g.SetTag(repo.Owner, repo.Name, tag, commit)
			g.SetRelease(repo.Owner, repo.Name, tag, body, fake.ReleaseFile{Name: asset, Data: []byte(content)})
		},
	})
}

// combined is the state Gitea combines contexts into: the worst of them.
func combined(contexts map[string]string) string {
	for _, state := range []string{ports.CheckError, ports.CheckFailure, ports.CheckPending} {
		for _, s := range contexts {
			if s == state {
				return state
			}
		}
	}
	return ports.CheckSuccess
}
