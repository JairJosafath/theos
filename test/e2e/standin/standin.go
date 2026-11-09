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

// Package standin keeps the e2e suite on stand-in agents: it needs no model
// keys and spends no tokens. Check is the guard the suite's BeforeSuite runs
// over its fixture; it has no build tag, so make test runs its unit test.
package standin

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// Check fails for every Agent that doesn't run on the stand-in, with the harness
// set explicitly, or that references a Secret: its model credentials or one of
// its environment variables.
func Check(agents []c4v1beta1.Agent) error {
	var errs []error
	for _, a := range agents {
		if a.Spec.Harness != c4v1beta1.HarnessStandIn {
			errs = append(errs, fmt.Errorf("agent %s: harness is %q, not %q", a.Name, a.Spec.Harness,
				c4v1beta1.HarnessStandIn))
		}
		if a.Spec.CredentialsSecretRef != nil {
			errs = append(errs, fmt.Errorf("agent %s: references the credentials Secret %s", a.Name,
				a.Spec.CredentialsSecretRef.Name))
		}
		for _, e := range a.Spec.Env {
			if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
				errs = append(errs, fmt.Errorf("agent %s: env %s references the Secret %s", a.Name, e.Name,
					e.ValueFrom.SecretKeyRef.Name))
			}
		}
	}
	return errors.Join(errs...)
}

// Agents reads every Agent in the YAML files under dir.
func Agents(dir string) ([]c4v1beta1.Agent, error) {
	var agents []c4v1beta1.Agent
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (!strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml")) {
			return err
		}
		f, err := os.Open(p) //nolint:gosec // a YAML file under the directory it walks
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		dec := utilyaml.NewYAMLOrJSONDecoder(f, 4096)
		for {
			var obj unstructured.Unstructured
			if err := dec.Decode(&obj.Object); errors.Is(err, io.EOF) {
				return nil
			} else if err != nil {
				return fmt.Errorf("%s: %w", p, err)
			}
			if obj.GetKind() != "Agent" || obj.GroupVersionKind().Group != c4v1beta1.GroupVersion.Group {
				continue
			}
			var a c4v1beta1.Agent
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &a); err != nil {
				return fmt.Errorf("%s: agent %s: %w", p, obj.GetName(), err)
			}
			agents = append(agents, a)
		}
	})
	return agents, err
}
