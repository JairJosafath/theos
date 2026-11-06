/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
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
	"regexp"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

// The chart's Chat (docs/plans/done/chat-matrix.md, step 3): theos-chat with
// config/chat's RBAC, the operator's --chat-homeserver, and the in-cluster
// homeserver with federation and registration off, its image pinned by digest
// and its accounts made by a Job.
const (
	chatName          = release + "-chat"
	matrixName        = release + "-matrix"
	chatHomeserverArg = "--chat-homeserver="
	chatOn            = "chat.enabled=true"
	homeserverOn      = "chat.homeserver.enabled=true"
)

// renderChat renders the chart with the sets, and returns stdout, or stderr
// when helm fails.
func renderChat(helm string, sets ...string) ([]byte, error) {
	args := make([]string, 0, 5+2*len(sets))
	args = append(args, "template", release, chartDir, "--namespace", releaseNS)
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(helm, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stderr.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// objects decodes the built-in kinds in manifests by kind/namespace/name; it
// skips the others (CRDs, theos.factory.io objects).
func objects(t *testing.T, manifests []byte) map[string]runtime.Object {
	t.Helper()
	out := map[string]runtime.Object{}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(manifests)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		obj, gvk, err := scheme.Codecs.UniversalDeserializer().Decode(doc, nil, nil)
		if runtime.IsNotRegisteredError(err) {
			continue
		}
		if err != nil {
			t.Fatalf("decode: %v\n%s", err, doc)
		}
		m, ok := obj.(interface {
			GetName() string
			GetNamespace() string
		})
		if !ok {
			continue
		}
		out[gvk.Kind+"/"+m.GetNamespace()+"/"+m.GetName()] = obj
	}
}

func mustRender(t *testing.T, helm string, sets ...string) []byte {
	t.Helper()
	out, err := renderChat(helm, sets...)
	if err != nil {
		t.Fatalf("helm template --set %v: %v\n%s", sets, err, out)
	}
	return out
}

// TestChatChartIsOffByDefault: without chat.enabled nothing of Chat renders,
// and the operator has no --chat-homeserver.
func TestChatChartIsOffByDefault(t *testing.T) {
	helm := helmBinary(t)
	out := mustRender(t, helm)
	for key := range objects(t, out) {
		if strings.HasSuffix(key, "/"+chatName) || strings.Contains(key, "/"+matrixName) {
			t.Errorf("the default chart renders %s", key)
		}
	}
	if got := managerArg(t, "the default chart", out, chatHomeserverArg); got != "" {
		t.Errorf("the default chart's manager has %q", got)
	}
}

// TestChatChartRunsTheosChatWithConfigChatsRBAC: chat.enabled installs
// theos-chat on the operator image, as its own ServiceAccount, bound to a
// ClusterRole whose rules are config/chat/rbac.yaml's.
func TestChatChartRunsTheosChatWithConfigChatsRBAC(t *testing.T) {
	helm := helmBinary(t)
	out := mustRender(t, helm, chatOn)
	objs := objects(t, out)

	role, ok := objs["ClusterRole//"+chatName].(*rbacv1.ClusterRole)
	if !ok {
		t.Fatalf("the chart renders no ClusterRole %s", chatName)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "chat", "rbac.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want, ok := objects(t, b)["ClusterRole//theos-chat"].(*rbacv1.ClusterRole)
	if !ok {
		t.Fatal("config/chat/rbac.yaml has no ClusterRole theos-chat")
	}
	if !reflect.DeepEqual(role.Rules, want.Rules) {
		t.Errorf("the chart's ClusterRole %s differs from config/chat's:\n got %+v\nwant %+v",
			chatName, role.Rules, want.Rules)
	}

	binding, ok := objs["ClusterRoleBinding//"+chatName].(*rbacv1.ClusterRoleBinding)
	if !ok {
		t.Fatalf("the chart renders no ClusterRoleBinding %s", chatName)
	}
	wantSubjects := []rbacv1.Subject{{Kind: "ServiceAccount", Name: chatName, Namespace: releaseNS}}
	if binding.RoleRef.Name != chatName || !reflect.DeepEqual(binding.Subjects, wantSubjects) {
		t.Errorf("ClusterRoleBinding %s binds %s to %+v", chatName, binding.RoleRef.Name, binding.Subjects)
	}
	if _, ok := objs["ServiceAccount/"+releaseNS+"/"+chatName].(*corev1.ServiceAccount); !ok {
		t.Errorf("the chart renders no ServiceAccount %s", chatName)
	}

	d, ok := objs["Deployment/"+releaseNS+"/"+chatName].(*appsv1.Deployment)
	if !ok {
		t.Fatalf("the chart renders no Deployment %s", chatName)
	}
	pod := d.Spec.Template.Spec
	if pod.ServiceAccountName != chatName {
		t.Errorf("theos-chat runs as %q, want %s", pod.ServiceAccountName, chatName)
	}
	if *d.Spec.Replicas != 1 {
		t.Errorf("theos-chat has %d replicas, want 1: it elects no leader", *d.Spec.Replicas)
	}
	manager, ok := objs["Deployment/"+releaseNS+"/"+release+"-controller-manager"].(*appsv1.Deployment)
	if !ok {
		t.Fatal("the chart renders no manager")
	}
	c := pod.Containers[0]
	operatorImage := manager.Spec.Template.Spec.Containers[0].Image
	if c.Image != operatorImage || !reflect.DeepEqual(c.Command, []string{"/theos-chat"}) {
		t.Errorf("theos-chat runs %v on %s, want /theos-chat on the operator's image", c.Command, c.Image)
	}
	nonRoot := pod.SecurityContext != nil && pod.SecurityContext.RunAsNonRoot != nil && *pod.SecurityContext.RunAsNonRoot
	readOnly := c.SecurityContext != nil && c.SecurityContext.ReadOnlyRootFilesystem != nil &&
		*c.SecurityContext.ReadOnlyRootFilesystem
	if !nonRoot || !readOnly {
		t.Errorf("theos-chat must run as non-root on a read-only root filesystem")
	}
	checkProbes(t, "the chart with chat", out, chatName)

	// No homeserver of its own and none named: each Chat names its own.
	if got := managerArg(t, "the chart with chat", out, chatHomeserverArg); got != "" {
		t.Errorf("the manager has %q with no homeserver to name", got)
	}
	if _, ok := objs["StatefulSet/"+releaseNS+"/"+matrixName]; ok {
		t.Errorf("the chart renders the homeserver without chat.homeserver.enabled")
	}
	out = mustRender(t, helm, chatOn, "chat.homeserverURL=https://matrix.example.com")
	wantArg := chatHomeserverArg + "https://matrix.example.com"
	if got := managerArg(t, "the chart", out, chatHomeserverArg); got != wantArg {
		t.Errorf("with chat.homeserverURL the manager has %q, want %q", got, wantArg)
	}
}

// chatArg is theos-chat's argument with prefix, "" for none.
func chatArg(t *testing.T, where string, manifests []byte, prefix string) string {
	t.Helper()
	d, ok := objects(t, manifests)["Deployment/"+releaseNS+"/"+chatName].(*appsv1.Deployment)
	if !ok {
		t.Fatalf("%s renders no Deployment %s", where, chatName)
	}
	for _, a := range d.Spec.Template.Spec.Containers[0].Args {
		if strings.HasPrefix(a, prefix) {
			return a
		}
	}
	return ""
}

// TestChatChartLinksToTheUI: theos-chat's --ui-url, which its posts link the
// UI's object pages under, is chat.uiURL, else the UI's own address: behind
// an Ingress its first allowed host, else the port-forward's localhost; with
// the UI off, none.
func TestChatChartLinksToTheUI(t *testing.T) {
	helm := helmBinary(t)
	const uiURLArg = "--ui-url="
	for _, tt := range []struct {
		name string
		sets []string
		want string
	}{
		{"the port-forward", nil, uiURLArg + "http://localhost:8080"},
		{"an Ingress", []string{"ui.allowedHosts={factory.example.com}"}, uiURLArg + "https://factory.example.com"},
		{"an Ingress without TLS", []string{"ui.allowedHosts={factory.example.com}", "ui.sessionBinaryScheme=http"},
			uiURLArg + "http://factory.example.com"},
		{"chat.uiURL", []string{"ui.allowedHosts={factory.example.com}", "chat.uiURL=https://theos.example.com/"},
			uiURLArg + "https://theos.example.com"},
		{"chat.uiURL without the UI", []string{"ui.enabled=false", "chat.uiURL=https://theos.example.com/ui"},
			uiURLArg + "https://theos.example.com/ui"},
		{"no UI", []string{"ui.enabled=false"}, ""},
	} {
		out := mustRender(t, helm, append([]string{chatOn}, tt.sets...)...)
		if got := chatArg(t, tt.name, out, uiURLArg); got != tt.want {
			t.Errorf("%s: theos-chat has %q, want %q", tt.name, got, tt.want)
		}
	}
}

var pinned = regexp.MustCompile(`^[^@]+:[^@]+@sha256:[0-9a-f]{64}$`)

// TestChatChartHomeserver: chat.homeserver.enabled installs Tuwunel, pinned
// by digest, federation and registration off, its shared secret made in the
// cluster; the operator's --chat-homeserver is its Service; and the accounts
// Job runs files/matrix-accounts.sh for the bot's namespaces and the people.
func TestChatChartHomeserver(t *testing.T) {
	helm := helmBinary(t)
	out := mustRender(t, helm, chatOn, homeserverOn, "chat.homeserver.serverName=chat.example.internal",
		"chat.homeserver.persistence.storageClassName=fast", "chat.homeserver.accounts={ada,grace}",
		"gitea.projects={apps}", "chat.homeserver.bot.namespaces={shop,apps}")
	objs := objects(t, out)

	url := "http://" + matrixName + "." + releaseNS + ".svc:8008"
	if got := managerArg(t, "the chart", out, chatHomeserverArg); got != chatHomeserverArg+url {
		t.Errorf("the manager has %q, want %q", got, chatHomeserverArg+url)
	}
	out2 := mustRender(t, helm, chatOn, homeserverOn, "chat.homeserverURL=https://matrix.example.com")
	if got := managerArg(t, "the chart", out2, chatHomeserverArg); got != chatHomeserverArg+"https://matrix.example.com" {
		t.Errorf("chat.homeserverURL doesn't win over the in-cluster homeserver: %q", got)
	}

	checkHomeserver(t, objs)
	checkAccountsJob(t, objs, url)
}

// checkHomeserver requires the homeserver's StatefulSet, pinned by digest,
// with federation and registration off, its shared secret from its Secret,
// and its Service.
func checkHomeserver(t *testing.T, objs map[string]runtime.Object) {
	t.Helper()
	sts, ok := objs["StatefulSet/"+releaseNS+"/"+matrixName].(*appsv1.StatefulSet)
	if !ok {
		t.Fatalf("the chart renders no StatefulSet %s", matrixName)
	}
	pod := sts.Spec.Template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Errorf("the homeserver's pod mounts an API token; only its credentials container may")
	}
	var hs *corev1.Container
	for i := range pod.Containers {
		if pod.Containers[i].Name == "homeserver" {
			hs = &pod.Containers[i]
		}
	}
	if hs == nil {
		t.Fatal("the StatefulSet has no homeserver container")
	}
	if !pinned.MatchString(hs.Image) {
		t.Errorf("the homeserver's image %s is not pinned as name:tag@sha256:digest", hs.Image)
	}
	env := map[string]corev1.EnvVar{}
	for _, e := range hs.Env {
		env[e.Name] = e
	}
	for name, want := range map[string]string{
		"TUWUNEL_SERVER_NAME":        "chat.example.internal",
		"TUWUNEL_ALLOW_FEDERATION":   "false",
		"TUWUNEL_ALLOW_REGISTRATION": "false",
	} {
		if got := env[name].Value; got != want {
			t.Errorf("the homeserver's %s is %q, want %q", name, got, want)
		}
	}
	ref := env["TUWUNEL_REGISTRATION_SHARED_SECRET"].ValueFrom
	if ref == nil || ref.SecretKeyRef == nil || ref.SecretKeyRef.Name != matrixName+"-registration" {
		t.Errorf("the homeserver's shared secret isn't read from Secret %s-registration: %+v", matrixName, ref)
	}
	if len(sts.Spec.VolumeClaimTemplates) != 1 ||
		sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName == nil ||
		*sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName != "fast" {
		t.Errorf("the homeserver's volume doesn't take chat.homeserver.persistence.storageClassName")
	}
	if _, ok := objs["Service/"+releaseNS+"/"+matrixName].(*corev1.Service); !ok {
		t.Errorf("the chart renders no Service %s", matrixName)
	}
}

// checkAccountsJob requires the accounts Job running files/matrix-accounts.sh
// for the bot's namespaces and the people, and a Role in each of those
// namespaces for the bot's token Secret alone.
func checkAccountsJob(t *testing.T, objs map[string]runtime.Object, url string) {
	t.Helper()

	job, ok := objs["Job/"+releaseNS+"/"+matrixName+"-accounts"].(*batchv1.Job)
	if !ok {
		t.Fatalf("the chart renders no Job %s-accounts", matrixName)
	}
	script, err := os.ReadFile(filepath.Join(chartDir, "files", "matrix-accounts.sh"))
	if err != nil {
		t.Fatal(err)
	}
	jc := job.Spec.Template.Spec.Containers[0]
	if len(jc.Args) != 1 || jc.Args[0] != string(script) {
		t.Errorf("the accounts Job doesn't run files/matrix-accounts.sh as it is")
	}
	jobEnv := map[string]string{}
	for _, e := range jc.Env {
		jobEnv[e.Name] = e.Value
	}
	for name, want := range map[string]string{
		"NAMESPACES": "apps shop", "ACCOUNTS": "ada grace", "TOKEN_SECRET": "matrix-bot",
		"HOMESERVER": url, "SERVER_NAME": "chat.example.internal",
	} {
		if got := jobEnv[name]; got != want {
			t.Errorf("the accounts Job's %s is %q, want %q", name, got, want)
		}
	}
	for _, ns := range []string{"apps", "shop"} {
		role, ok := objs["Role/"+ns+"/"+matrixName+"-accounts"].(*rbacv1.Role)
		if !ok {
			t.Errorf("no Role %s-accounts in %s, where the Job puts the bot's token", matrixName, ns)
			continue
		}
		if len(role.Rules) != 2 || !reflect.DeepEqual(role.Rules[1].ResourceNames, []string{"matrix-bot"}) {
			t.Errorf("Role %s-accounts in %s has %+v, want create and get on matrix-bot", matrixName, ns, role.Rules)
		}
	}
}

// TestChatChartRefuses: settings the chart can't honour fail the render.
func TestChatChartRefuses(t *testing.T) {
	helm := helmBinary(t)
	for _, sets := range [][]string{
		{homeserverOn},
		{chatOn, "chat.homeserverURL=matrix.example.com"},
		{chatOn, "chat.uiURL=theos.example.com"},
		{chatOn, "chat.uiURL=https://theos.example.com/?area=work"},
		{chatOn, "chat.uiURL=https://ada:secret@theos.example.com"},
		{chatOn, homeserverOn, "chat.homeserver.accounts={Ada}"},
		{chatOn, homeserverOn, "chat.homeserver.image.digest=latest"},
		{chatOn, homeserverOn, "chat.homeserver.bot.localpart=The Bot"},
	} {
		if out, err := renderChat(helm, sets...); err == nil {
			t.Errorf("helm template --set %v renders (%d bytes); the chart must refuse it", sets, len(out))
		}
	}
}
