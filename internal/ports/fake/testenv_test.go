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

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

func TestTestEnvironmentsContract(t *testing.T) {
	for _, hold := range []bool{false, true} {
		f := &fake.TestEnvironments{Hold: hold}
		envs := 0
		env := contract.TestEnvironmentsEnv{
			Provider: f,
			Environment: func(*testing.T) ports.Environment {
				envs++
				return ports.Environment{Namespace: fmt.Sprintf("apps-%d", envs), Name: "browser",
					Member: model.TestEnvMemberLocal}
			},
			Places: func(_ *testing.T, e ports.Environment) int { return f.Held(e) },
			Fail:   func(_ *testing.T, e ports.Environment, id string) { f.Fail(e, id, "the pod failed") },
		}
		name := "ready at once"
		if hold {
			env.Ready = func(_ *testing.T, e ports.Environment, id string) { f.Ready(e, id) }
			name = "held"
		}
		t.Run(name, func(t *testing.T) { contract.TestEnvironments(t, env) })
	}
}
