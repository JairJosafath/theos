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

package standin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

func agent(name string, harness c4v1beta1.AgentHarness, edit func(*c4v1beta1.AgentSpec)) c4v1beta1.Agent {
	a := c4v1beta1.Agent{ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: c4v1beta1.AgentSpec{Image: "theos/agent-base:dev", Harness: harness}}
	if edit != nil {
		edit(&a.Spec)
	}
	return a
}

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		name  string
		agent c4v1beta1.Agent
		want  string // empty: passes
	}{
		{"stand-in", agent("ok", c4v1beta1.HarnessStandIn, nil), ""},
		{"claude-code", agent("claude", c4v1beta1.HarnessClaudeCode, nil),
			`agent claude: harness is "claude-code", not "stand-in"`},
		{"harness left to the default", agent("unset", "", nil), `agent unset: harness is "", not "stand-in"`},
		{"credentials Secret", agent("keyed", c4v1beta1.HarnessStandIn, func(s *c4v1beta1.AgentSpec) {
			s.CredentialsSecretRef = &c4v1beta1.LocalRef{Name: "claude-credentials"}
		}), "agent keyed: references the credentials Secret claude-credentials"},
		{"Secret in env", agent("env", c4v1beta1.HarnessStandIn, func(s *c4v1beta1.AgentSpec) {
			s.Env = []corev1.EnvVar{{Name: "CLAUDE_CODE_OAUTH_TOKEN", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "claude"}, Key: "token"}}}}
		}), "agent env: env CLAUDE_CODE_OAUTH_TOKEN references the Secret claude"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := Check([]c4v1beta1.Agent{c.agent})
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("Check: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("Check: got %v, want %q", err, c.want)
			}
		})
	}
}

// TestFixture runs the guard over the e2e fixture, as the suite's BeforeSuite
// does, so a fixture change that breaks it fails make test first.
func TestFixture(t *testing.T) {
	agents, err := Agents("../testdata/apps")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) == 0 {
		t.Fatal("the fixture has no Agent")
	}
	if err := Check(agents); err != nil {
		t.Fatal(err)
	}
}

func TestAgentsReadsOnlyAgents(t *testing.T) {
	dir := t.TempDir()
	yaml := `apiVersion: theos.factory.io/v1beta1
kind: Framework
metadata: {name: hello-html}
spec: {supportedTypes: [static-site], template: {url: http://gitea/templates/hello-html, ref: v1.0.0}}
---
apiVersion: theos.factory.io/v1beta1
kind: Agent
metadata: {name: claude}
spec: {image: agent:dev, harness: claude-code, credentialsSecretRef: {name: claude-credentials}}
`
	if err := os.WriteFile(filepath.Join(dir, "factory.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	agents, err := Agents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].Name != "claude" {
		t.Fatalf("Agents: got %+v, want the Agent claude", agents)
	}
	err = Check(agents)
	for _, want := range []string{`harness is "claude-code"`, "credentials Secret claude-credentials"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Check: got %v, want %q", err, want)
		}
	}
}
