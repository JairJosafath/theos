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
	"crypto/rand"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

const (
	uiName      = release + "-ui"
	uiRBAC      = "templates/ui/rbac.yaml"
	uiDeploy    = "templates/ui/deployment.yaml"
	c4Group     = "c4.factory.io"
	uiActionsOn = "ui.actions.enabled=true"
	uiIngress   = "ui.allowedHosts={factory.example.com}"
	oidcSecret  = "c4-factory-ui-oidc"
)

// uiAuthOIDC are the settings ui.auth.oidc needs.
var uiAuthOIDC = []string{
	"ui.auth.oidc.issuerURL=https://issuer.example",
	"ui.auth.oidc.clientID=c4-factory-ui",
	"ui.auth.oidc.existingSecret=" + oidcSecret,
}

// withOIDC is actions on behind an Ingress with ui.auth.oidc, and sets.
func withOIDC(sets ...string) []string {
	return append(append([]string{uiActionsOn, uiIngress}, uiAuthOIDC...), sets...)
}

// uiRender is what the chart renders for the UI's writes: each ClusterRole's
// rules that write (no get, list or watch), the ClusterRoles bound to the
// UI's ServiceAccount and to groups, and the Deployment's UI_ACTIONS and
// environment.
type uiRender struct {
	writes    map[string][]rbacv1.PolicyRule
	bound     []string
	groups    map[string][]string
	uiActions string
	env       map[string]corev1.EnvVar
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
	impersonate := []rbacv1.PolicyRule{{
		APIGroups: []string{""}, Resources: []string{"users", "groups"}, Verbs: []string{"impersonate"},
	}}
	oidcWrites := map[string][]rbacv1.PolicyRule{uiName + "-actions": actions, uiName + "-impersonator": impersonate}
	oidcBound := []string{uiName + "-health", uiName + "-impersonator", uiName + "-reader"}
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
			sets: []string{uiIngress},
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
			sets: withOIDC(),
			want: uiRender{writes: oidcWrites, bound: oidcBound, uiActions: "oidc"},
		},
		{
			name: "actions on with OIDC over port-forward",
			sets: append([]string{uiActionsOn}, uiAuthOIDC...),
			want: uiRender{writes: oidcWrites, bound: oidcBound, uiActions: "oidc"},
		},
		{
			name: "actions on with OIDC and actionGroups",
			sets: withOIDC("ui.auth.oidc.actionGroups={factory-actors,leads}"),
			want: uiRender{
				writes:    oidcWrites,
				bound:     oidcBound,
				groups:    map[string][]string{uiName + "-actions": {"oidc:factory-actors", "oidc:leads"}},
				uiActions: "oidc",
			},
		},
		{
			name: "actionGroups with another groupsPrefix",
			sets: withOIDC("ui.auth.oidc.actionGroups={factory-actors}", "ui.auth.oidc.groupsPrefix=sso:"),
			want: uiRender{
				writes:    oidcWrites,
				bound:     oidcBound,
				groups:    map[string][]string{uiName + "-actions": {"sso:factory-actors"}},
				uiActions: "oidc",
			},
		},
		{
			name: "ui.auth.oidc with actions off",
			sets: append([]string{uiIngress}, uiAuthOIDC...),
			want: uiRender{
				writes: map[string][]rbacv1.PolicyRule{uiName + "-brief-writer": briefWriter},
				bound:  append([]string{uiName + "-brief-writer"}, reads...),
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
			if len(got.groups)+len(c.want.groups) > 0 && !reflect.DeepEqual(got.groups, c.want.groups) {
				t.Errorf("ClusterRoles bound to groups: got %v, want %v", got.groups, c.want.groups)
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
	out, err := helmTemplate(helm, uiActionsOn, uiIngress)
	if err == nil {
		t.Fatalf("the render succeeded:\n%s", out)
	}
	if !strings.Contains(out, "ui.auth.oidc") {
		t.Errorf("the render failed without naming ui.auth.oidc:\n%s", out)
	}
}

// TestUIOIDCNeedsItsSettings: ui.auth.oidc without issuerURL, clientID or
// existingSecret fails the render, naming the missing one.
func TestUIOIDCNeedsItsSettings(t *testing.T) {
	helm := helmBinary(t)
	for i, set := range uiAuthOIDC {
		field, _, _ := strings.Cut(set, "=")
		t.Run(field, func(t *testing.T) {
			others := slices.Delete(slices.Clone(uiAuthOIDC), i, i+1)
			out, err := helmTemplate(helm, append([]string{uiActionsOn, uiIngress}, others...)...)
			if err == nil {
				t.Fatalf("the render succeeded:\n%s", out)
			}
			if !strings.Contains(out, field+" is required") {
				t.Errorf("the render failed without naming %s:\n%s", field, out)
			}
		})
	}
}

// TestUIOIDCSettings: the UI server gets ui.auth.oidc as OIDC_* variables,
// the defaults where unset, and an empty prefix stays empty.
func TestUIOIDCSettings(t *testing.T) {
	helm := helmBinary(t)
	values := func(r uiRender) map[string]string {
		out := map[string]string{}
		for name, e := range r.env {
			if strings.HasPrefix(name, "OIDC_") && e.ValueFrom == nil {
				out[name] = e.Value
			}
		}
		return out
	}
	defaults := map[string]string{
		"OIDC_ISSUER_URL":      "https://issuer.example",
		"OIDC_CLIENT_ID":       "c4-factory-ui",
		"OIDC_SCOPES":          "openid email profile",
		"OIDC_USERNAME_CLAIM":  "email",
		"OIDC_USERNAME_PREFIX": "oidc:",
		"OIDC_GROUPS_CLAIM":    "groups",
		"OIDC_GROUPS_PREFIX":   "oidc:",
	}
	if got := values(renderUI(t, helm, withOIDC()...)); !reflect.DeepEqual(got, defaults) {
		t.Errorf("defaults:\n got %v\nwant %v", got, defaults)
	}
	set := maps.Clone(defaults)
	set["OIDC_REDIRECT_URL"] = "https://factory.example.com/ui/callback"
	set["OIDC_SCOPES"] = "openid email groups"
	set["OIDC_USERNAME_CLAIM"] = "preferred_username"
	set["OIDC_USERNAME_PREFIX"] = ""
	set["OIDC_GROUPS_CLAIM"] = "roles"
	set["OIDC_GROUPS_PREFIX"] = "sso:"
	got := values(renderUI(t, helm, withOIDC(
		"ui.auth.oidc.redirectURL=https://factory.example.com/ui/callback",
		"ui.auth.oidc.scopes={openid,email,groups}",
		"ui.auth.oidc.usernameClaim=preferred_username",
		"ui.auth.oidc.usernamePrefix=",
		"ui.auth.oidc.groupsClaim=roles",
		"ui.auth.oidc.groupsPrefix=sso:",
	)...))
	if !reflect.DeepEqual(got, set) {
		t.Errorf("set:\n got %v\nwant %v", got, set)
	}
}

// TestUIOIDCNeverRendersTheClientSecret: the client secret reaches the UI
// server only from the Secret existingSecret names, never as a value, even
// when someone puts one in values.
func TestUIOIDCNeverRendersTheClientSecret(t *testing.T) {
	helm := helmBinary(t)
	secret := rand.Text()
	sets := withOIDC("ui.auth.oidc.clientSecret="+secret, "ui.auth.oidc.existingSecretKey=secret")
	out, err := helmTemplate(helm, sets...)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	if strings.Contains(out, secret) {
		t.Errorf("the render holds the client secret from values")
	}
	r := renderUI(t, helm, sets...)
	want := &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: oidcSecret}, Key: "secret"}
	got := r.env["OIDC_CLIENT_SECRET"]
	if got.Value != "" || got.ValueFrom == nil || !reflect.DeepEqual(got.ValueFrom.SecretKeyRef, want) {
		t.Errorf("OIDC_CLIENT_SECRET: got %+v, want only a secretKeyRef to %+v", got, want)
	}
	for name, e := range r.env {
		if name != "OIDC_CLIENT_SECRET" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			t.Errorf("%s also reads Secret %s", name, e.ValueFrom.SecretKeyRef.Name)
		}
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
	r := uiRender{writes: map[string][]rbacv1.PolicyRule{}, groups: map[string][]string{}, env: map[string]corev1.EnvVar{}}
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
				if s.Kind == rbacv1.GroupKind {
					r.groups[o.RoleRef.Name] = append(r.groups[o.RoleRef.Name], s.Name)
				}
			}
		case *appsv1.Deployment:
			for _, e := range o.Spec.Template.Spec.Containers[0].Env {
				r.env[e.Name] = e
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
