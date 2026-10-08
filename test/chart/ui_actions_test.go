/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chart

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	uiName      = release + "-ui"
	uiRBAC      = "templates/ui/rbac.yaml"
	uiDeploy    = "templates/ui/deployment.yaml"
	c4Group     = "c4.factory.io"
	uiAuthOIDC  = "ui.auth.oidc.issuerURL=https://issuer.example"
	uiActionsOn = "ui.actions.enabled=true"
)

// uiRender is what the chart renders for the UI's writes: each ClusterRole's
// rules that write (no get, list or watch), the ClusterRoles bound to the
// UI's ServiceAccount, and the Deployment's UI_ACTIONS.
type uiRender struct {
	writes    map[string][]rbacv1.PolicyRule
	bound     []string
	uiActions string
}

// TestUIActionsGrantExactlyTheTable renders the chart with ui.actions.enabled
// off and on, over port-forward and with ui.auth.oidc, and asserts the UI's
// write rules and bindings exactly (design.md, UI actions).
func TestUIActionsGrantExactlyTheTable(t *testing.T) {
	helm := helmBinary(t)
	briefWriter := []rbacv1.PolicyRule{{
		APIGroups: []string{c4Group}, Resources: []string{"briefs"}, Verbs: []string{"create", "patch"},
	}}
	actions := []rbacv1.PolicyRule{
		{APIGroups: []string{c4Group}, Resources: []string{"briefs", "tasks"}, Verbs: []string{"create"}},
		{
			APIGroups: []string{c4Group},
			Resources: []string{"briefs", "crews", "containers", "standards"},
			Verbs:     []string{"patch"},
		},
	}
	reads := []string{uiName + "-health", uiName + "-reader"}
	cases := []struct {
		name string
		sets []string
		want uiRender
	}{
		{
			name: "actions off",
			want: uiRender{
				writes: map[string][]rbacv1.PolicyRule{uiName + "-brief-writer": briefWriter},
				bound:  append([]string{uiName + "-brief-writer"}, reads...),
			},
		},
		{
			name: "actions off behind an Ingress",
			sets: []string{"ui.allowedHosts={factory.example.com}"},
			want: uiRender{
				writes: map[string][]rbacv1.PolicyRule{uiName + "-brief-writer": briefWriter},
				bound:  append([]string{uiName + "-brief-writer"}, reads...),
			},
		},
		{
			name: "actions on over port-forward",
			sets: []string{uiActionsOn},
			want: uiRender{
				writes:    map[string][]rbacv1.PolicyRule{uiName + "-actions": actions},
				bound:     append([]string{uiName + "-actions"}, reads...),
				uiActions: "service-account",
			},
		},
		{
			name: "actions on with OIDC behind an Ingress",
			sets: []string{uiActionsOn, "ui.allowedHosts={factory.example.com}", uiAuthOIDC},
			want: uiRender{
				writes:    map[string][]rbacv1.PolicyRule{uiName + "-actions": actions},
				bound:     reads,
				uiActions: "oidc",
			},
		},
		{
			name: "actions on with an empty ui.auth.oidc",
			sets: []string{uiActionsOn, "ui.auth.oidc.issuerURL=null"},
			want: uiRender{
				writes:    map[string][]rbacv1.PolicyRule{uiName + "-actions": actions},
				bound:     reads,
				uiActions: "oidc",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderUI(t, helm, c.sets...)
			if !reflect.DeepEqual(got.writes, c.want.writes) {
				t.Errorf("write rules:\n got %+v\nwant %+v", got.writes, c.want.writes)
			}
			if !reflect.DeepEqual(got.bound, c.want.bound) {
				t.Errorf("ClusterRoles bound to the ServiceAccount: got %v, want %v", got.bound, c.want.bound)
			}
			if got.uiActions != c.want.uiActions {
				t.Errorf("UI_ACTIONS: got %q, want %q", got.uiActions, c.want.uiActions)
			}
		})
	}
}

// TestUIActionsBehindAnIngressNeedOIDC: ui.actions.enabled with
// ui.allowedHosts and no ui.auth.oidc fails the render, naming ui.auth.oidc.
func TestUIActionsBehindAnIngressNeedOIDC(t *testing.T) {
	helm := helmBinary(t)
	out, err := helmTemplate(helm, uiActionsOn, "ui.allowedHosts={factory.example.com}")
	if err == nil {
		t.Fatalf("the render succeeded:\n%s", out)
	}
	if !strings.Contains(out, "ui.auth.oidc") {
		t.Errorf("the render failed without naming ui.auth.oidc:\n%s", out)
	}
}

func helmBinary(t *testing.T) string {
	t.Helper()
	helm := os.Getenv("HELM")
	if helm == "" {
		helm = filepath.Join("..", "..", "bin", "helm")
	}
	if _, err := os.Stat(helm); err != nil {
		t.Skipf("no helm at %s (make install-helm, or run make test): %v", helm, err)
	}
	return helm
}

// helmTemplate renders the UI's RBAC and Deployment with the values sets, and
// returns stdout, or stderr when helm fails.
func helmTemplate(helm string, sets ...string) (string, error) {
	args := make([]string, 0, 9+2*len(sets))
	args = append(args, "template", release, chartDir, "--namespace", releaseNS,
		"--show-only", uiRBAC, "--show-only", uiDeploy)
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(helm, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stderr.String(), err
	}
	return stdout.String(), nil
}

func renderUI(t *testing.T, helm string, sets ...string) uiRender {
	t.Helper()
	out, err := helmTemplate(helm, sets...)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	r := uiRender{writes: map[string][]rbacv1.PolicyRule{}}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(out)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(doc, nil, nil)
		if err != nil {
			t.Fatalf("decode: %v\n%s", err, doc)
		}
		switch o := obj.(type) {
		case *rbacv1.ClusterRole:
			for _, rule := range o.Rules {
				if !isReadOnly(rule) {
					r.writes[o.Name] = append(r.writes[o.Name], rule)
				}
			}
		case *rbacv1.ClusterRoleBinding:
			for _, s := range o.Subjects {
				if s.Kind == rbacv1.ServiceAccountKind && s.Name == uiName && s.Namespace == releaseNS {
					r.bound = append(r.bound, o.RoleRef.Name)
				}
			}
		case *appsv1.Deployment:
			for _, e := range o.Spec.Template.Spec.Containers[0].Env {
				if e.Name == "UI_ACTIONS" {
					r.uiActions = e.Value
				}
			}
		}
	}
	slices.Sort(r.bound)
	return r
}

func isReadOnly(rule rbacv1.PolicyRule) bool {
	for _, v := range rule.Verbs {
		if v != "get" && v != "list" && v != "watch" {
			return false
		}
	}
	return true
}
