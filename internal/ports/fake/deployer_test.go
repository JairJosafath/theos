package fake_test

import (
	"context"
	"testing"

	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/contract"
	"github.com/JairJosafath/c4factory/internal/ports/fake"
)

func TestDeployerContract(t *testing.T) {
	for _, hold := range []bool{false, true} {
		d := &fake.Deployer{Hold: hold}
		targets := 0
		target := func(*testing.T) ports.Target {
			targets++
			return ports.Target{Name: "ns/target-" + string(rune('a'+targets)), Member: "fake"}
		}
		env := contract.DeployerEnv{Deployer: d, Target: target,
			Refused: func(t *testing.T) ports.Target {
				tt := target(t)
				d.Refuse(tt.Name)
				return tt
			},
			Deploys: func(_ *testing.T, tt ports.Target, app string) int { return d.Deploys(tt.Name, app) },
		}
		if hold {
			// A held deploy finishes when the test says so: here, as soon as
			// it is read back.
			env.Deployer = finishing{d}
		}
		name := "deployed at once"
		if hold {
			name = "held"
		}
		t.Run(name, func(t *testing.T) { contract.Deployer(t, env) })
	}
}

// finishing is a held fake whose deploys finish once State reads them.
type finishing struct{ *fake.Deployer }

func (f finishing) State(ctx context.Context, t ports.Target, app string) (ports.DeployState, error) {
	st, err := f.Deployer.State(ctx, t, app)
	if err == nil && st.Phase == ports.PhaseDeploying {
		st.Phase, st.Health = ports.PhaseDeployed, ports.HealthHealthy
		f.SetState(t.Name, app, st)
	}
	return st, err
}
