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
