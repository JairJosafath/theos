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

package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/theos/internal/model"
)

// TestPostgresRunsOnKubernetes checks the postgres Connector's manifests
// (#1471): a pinned, non-root Postgres in a StatefulSet with a volume and a
// readiness probe, a Service of the name DATABASE_URL uses, and the password
// only from Secret <container>-postgres, for the database and for the app.
func TestPostgresRunsOnKubernetes(t *testing.T) {
	_, result := renderCombo(t, "service", []side{
		{model.SideFramework, "python-fastapi"},
		{model.SideCompute, "small"},
		{model.SideDeployment, "k8s-manifests"},
		{model.SideConnector, "postgres"},
	})
	connector := string(result.Files["deploy/k8s/connector-postgres.yaml"].Data)
	assertContains(t, "deploy/k8s/connector-postgres.yaml", connector, []string{
		"kind: StatefulSet", "kind: Service", "name: sample-app-postgres\n",
		"DATABASE_URL: postgresql://sample_app@sample-app-postgres:5432/sample_app",
		"image: postgres:17.11-alpine@sha256:", "readinessProbe:", "runAsNonRoot: true", "volumeClaimTemplates:",
		"- name: POSTGRES_PASSWORD\n              valueFrom:\n                secretKeyRef:\n                  name: sample-app-postgres\n                  key: password",
	})
	assertContains(t, "deploy/k8s/deployment.yaml", string(result.Files["deploy/k8s/deployment.yaml"].Data), []string{
		"- name: PGPASSWORD\n              valueFrom:\n                secretKeyRef:\n                  name: sample-app-postgres\n                  key: password",
	})
	for _, p := range []string{"deploy/k8s/connector-postgres.yaml", "deploy/k8s/deployment.yaml"} {
		found, err := passwordLiterals(result.Files[p].Data)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(found) > 0 {
			t.Errorf("%s holds a password literal: %v", p, found)
		}
	}
}

// TestPasswordLiteralsFindsThem checks the check above can fail (#1471).
func TestPasswordLiteralsFindsThem(t *testing.T) {
	cases := []struct {
		name, manifest string
		want           bool
	}{
		{"env value", "kind: StatefulSet\nspec:\n  template:\n    spec:\n      containers:\n        - env:\n            - name: POSTGRES_PASSWORD\n              value: hunter2\n", true},
		{"in the URL", "kind: ConfigMap\ndata:\n  DATABASE_URL: postgresql://app:hunter2@db:5432/app\n", true},
		{"a Secret", "kind: Secret\nstringData:\n  password: hunter2\n", true},
		{"from a Secret", "kind: Deployment\nspec:\n  env:\n    - name: PGPASSWORD\n      valueFrom:\n        secretKeyRef: {name: db, key: password}\n", false},
		{"no password in the URL", "kind: ConfigMap\ndata:\n  DATABASE_URL: postgresql://app@db:5432/app\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			found, err := passwordLiterals([]byte(c.manifest))
			if err != nil {
				t.Fatal(err)
			}
			if got := len(found) > 0; got != c.want {
				t.Errorf("passwordLiterals = %v, want a literal found: %v", found, c.want)
			}
		})
	}
}

// urlPassword matches a connection URL with a password in it.
var urlPassword = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://[^/@:]+:[^/@]+@`)

// passwordLiterals lists the passwords written in the YAML documents of data:
// a Secret's data, an env entry *PASSWORD* with a value, a URL with a password.
func passwordLiterals(data []byte) ([]string, error) {
	var found []string
	var walk func(at string, v any)
	walk = func(at string, v any) {
		switch v := v.(type) {
		case map[string]any:
			if name, ok := v["name"].(string); ok && strings.Contains(strings.ToUpper(name), "PASSWORD") {
				if _, ok := v["value"]; ok {
					found = append(found, at+": env "+name+" has a value")
				}
			}
			for k, e := range v {
				walk(at+"."+k, e)
			}
		case []any:
			for i, e := range v {
				walk(fmt.Sprintf("%s[%d]", at, i), e)
			}
		case string:
			if urlPassword.MatchString(v) {
				found = append(found, at+": a URL with a password")
			}
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return found, nil
		}
		if err != nil {
			return nil, err
		}
		if doc["kind"] == "Secret" && (doc["data"] != nil || doc["stringData"] != nil) {
			found = append(found, "a Secret with data")
		}
		walk(fmt.Sprint(doc["kind"]), doc)
	}
}
