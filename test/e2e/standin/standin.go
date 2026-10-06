/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// Check fails for every Agent that doesn't run on the stand-in, with the harness
// set explicitly, or that references a Secret: its model credentials or one of
// its environment variables.
func Check(agents []c4v1alpha1.Agent) error {
	var errs []error
	for _, a := range agents {
		if a.Spec.Harness != c4v1alpha1.HarnessStandIn {
			errs = append(errs, fmt.Errorf("agent %s: harness is %q, not %q", a.Name, a.Spec.Harness,
				c4v1alpha1.HarnessStandIn))
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
func Agents(dir string) ([]c4v1alpha1.Agent, error) {
	var agents []c4v1alpha1.Agent
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (!strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml")) {
			return err
		}
		f, err := os.Open(p)
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
			if obj.GetKind() != "Agent" || obj.GroupVersionKind().Group != c4v1alpha1.GroupVersion.Group {
				continue
			}
			var a c4v1alpha1.Agent
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &a); err != nil {
				return fmt.Errorf("%s: agent %s: %w", p, obj.GetName(), err)
			}
			agents = append(agents, a)
		}
	})
	return agents, err
}
