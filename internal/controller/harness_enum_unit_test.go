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

package controller

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
)

// The harnesses are listed three times: Agent.spec.harness's enum in the
// CRD, the API's constants, and the runner's table, which is what a sandbox
// can run. A harness in the enum that the runner lacks is an Agent the API
// server takes and every run of which fails; one the enum lacks can't be
// asked for (#58).
func TestTheHarnessEnumIsTheRunnersTable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "theos.factory.io_agents.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties struct {
							Spec struct {
								Properties struct {
									Harness struct {
										Default string   `json:"default"`
										Enum    []string `json:"enum"`
									} `json:"harness"`
								} `json:"properties"`
							} `json:"spec"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := utilyaml.Unmarshal(data, &crd); err != nil {
		t.Fatal(err)
	}
	if len(crd.Spec.Versions) == 0 {
		t.Fatal("the Agent CRD has no version")
	}
	runner := agentrunner.Harnesses()
	for _, version := range crd.Spec.Versions {
		harness := version.Schema.OpenAPIV3Schema.Properties.Spec.Properties.Harness
		if !slices.Equal(slices.Sorted(slices.Values(harness.Enum)), slices.Sorted(slices.Values(runner))) {
			t.Errorf("Agent.spec.harness's enum is %q, and theos-agent runs %q", harness.Enum, runner)
		}
		if harness.Default != runner[0] {
			t.Errorf("Agent.spec.harness defaults to %q, and theos-agent to %q", harness.Default, runner[0])
		}
	}
	// The constants a controller compares with are harnesses too, and the
	// operator's own default is the runner's.
	for _, name := range []c4v1beta1.AgentHarness{c4v1beta1.HarnessClaudeCode, c4v1beta1.HarnessStandIn} {
		if !slices.Contains(runner, string(name)) {
			t.Errorf("the API's harness %q is none theos-agent runs (%q)", name, runner)
		}
	}
	if got := harness(&c4v1beta1.Agent{}); got != runner[0] {
		t.Errorf("an Agent that names no harness gets %q from the operator, and %q from theos-agent", got, runner[0])
	}
}
