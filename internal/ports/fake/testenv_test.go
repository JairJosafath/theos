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
