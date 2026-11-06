/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/localenv"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The device samples (docs/plans/done/test-environments.md, step 5): android's
// KVM and godot's GPU, runtime class and toleration reach the lease pod and
// the pods of the environment's CI pool as the samples give them, each
// device both requested and limited, as an extended resource must be.
func TestDeviceSamplesReachTheirPods(t *testing.T) {
	for _, tt := range []struct {
		file         string
		name         string
		device       corev1.ResourceName
		runtimeClass string
	}{
		{file: "testenvironment-android.yaml", name: "android", device: "devices.kubevirt.io/kvm"},
		{file: "testenvironment-godot.yaml", name: "godot", device: "nvidia.com/gpu", runtimeClass: "nvidia"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "config", "samples", "webshop", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			var env c4v1beta1.TestEnvironment
			if err := yaml.UnmarshalStrict(data, &env); err != nil {
				t.Fatal(err)
			}
			env.Namespace, env.UID = "webshop", "env-uid"
			local := env.Spec.Local
			if env.Name != tt.name || local == nil || local.RuntimeClassName != tt.runtimeClass {
				t.Fatalf("sample %s/%+v, want %s's local member under runtime class %q", env.Name, local, tt.name, tt.runtimeClass)
			}
			if q, ok := local.Devices[tt.device]; !ok || q.Cmp(resource.MustParse("1")) != 0 {
				t.Fatalf("devices %v, want one %s", local.Devices, tt.device)
			}

			lease, err := localenv.BuildPod(ports.LeaseRequest{ID: model.LeaseID(env.Name, "game-7-r1-implement"),
				Env: testEnvironment(&env, nil), Holder: "game-7", Token: "token", Deadline: time.Hour}, "agent:1")
			if err != nil {
				t.Fatal(err)
			}
			// Its CI pool, which the sample leaves at no pod, as a workflow's
			// runs-on: env-<name> asks for it.
			env.Spec.CI = &c4v1beta1.TestEnvironmentCI{Size: 1}
			provider := &c4v1beta1.GitProvider{ObjectMeta: objectMeta("webshop", "git"),
				Spec: c4v1beta1.GitProviderSpec{URL: "http://git.example:3000"}}
			in, clash := envRunnerPool(provider, []c4v1beta1.CIRunnerSpec{{Label: "base", Image: "theos/ci-runner-base:dev"}},
				&env, nil)
			if clash != "" {
				t.Fatal(clash)
			}
			ci := buildRunnerPod(in)

			for _, pod := range []*corev1.Pod{lease, ci} {
				spec := pod.Spec
				c := spec.Containers[0]
				if c.Image != local.Image {
					t.Errorf("%s: container image %s, want the sample's %s", pod.Name+pod.GenerateName, c.Image, local.Image)
				}
				for name, q := range local.Devices {
					if got := c.Resources.Requests[name]; got.Cmp(q) != 0 {
						t.Errorf("%s: requests %v, want %s %s", pod.Name+pod.GenerateName, c.Resources.Requests, name, q.String())
					}
					if got := c.Resources.Limits[name]; got.Cmp(q) != 0 {
						t.Errorf("%s: limits %v, want %s %s", pod.Name+pod.GenerateName, c.Resources.Limits, name, q.String())
					}
				}
				for name, q := range local.Resources.Requests {
					if got := c.Resources.Requests[name]; got.Cmp(q) != 0 {
						t.Errorf("%s: %s request %s, want the sample's %s", pod.Name+pod.GenerateName, name, got.String(), q.String())
					}
				}
				var runtimeClass string
				if spec.RuntimeClassName != nil {
					runtimeClass = *spec.RuntimeClassName
				}
				if runtimeClass != local.RuntimeClassName {
					t.Errorf("%s: runtimeClassName %q, want the sample's %q", pod.Name+pod.GenerateName, runtimeClass,
						local.RuntimeClassName)
				}
				if len(spec.Tolerations) != len(local.Tolerations) ||
					len(local.Tolerations) > 0 && !reflect.DeepEqual(spec.Tolerations, local.Tolerations) {
					t.Errorf("%s: tolerations %v, want the sample's %v", pod.Name+pod.GenerateName, spec.Tolerations,
						local.Tolerations)
				}
				if sc := c.SecurityContext; sc == nil || sc.Privileged == nil || *sc.Privileged {
					t.Errorf("%s: securityContext %+v, want not privileged: the device comes from its plugin",
						pod.Name+pod.GenerateName, sc)
				}
			}
		})
	}
}
