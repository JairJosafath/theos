/*
Copyright 2026.

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
package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// These tests check every milestone of docs/design.md against the Kind cluster
// that make e2e builds: Gitea seeded, the operator deployed, the webshop sample
// applied and the Session client image loaded. They never touch another cluster.

var (
	ctx        context.Context
	cancel     context.CancelFunc
	k8s        client.Client
	kubeCtx    string
	giteaURL   string
	giteaToken string
	forward    *exec.Cmd
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting C4 Factory e2e suite\n")
	RunSpecs(t, "C4 Factory e2e")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())
	cluster := os.Getenv("KIND_CLUSTER")
	if cluster == "" {
		cluster = "c4factory"
	}
	kubeCtx = "kind-" + cluster

	By("connecting to the Kind cluster " + cluster)
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: kubeCtx},
	).ClientConfig()
	Expect(err).NotTo(HaveOccurred())
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(c4v1alpha1.AddToScheme(scheme)).To(Succeed())
	k8s, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	By("checking the operator is running")
	Eventually(func(g Gomega) {
		var d appsv1.Deployment
		g.Expect(k8s.Get(ctx, types.NamespacedName{
			Namespace: "c4-factory-system", Name: "c4-factory-controller-manager",
		}, &d)).To(Succeed())
		g.Expect(d.Status.AvailableReplicas).To(BeEquivalentTo(1))
	}).WithTimeout(2 * time.Minute).Should(Succeed())

	By("forwarding a local port to Gitea")
	port := freePort()
	forward = exec.Command("kubectl", "--context", kubeCtx, "-n", "gitea",
		"port-forward", "svc/gitea", fmt.Sprintf("%d:3000", port))
	Expect(forward.Start()).To(Succeed())
	giteaURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	Eventually(func() error {
		resp, err := http.Get(giteaURL + "/api/healthz")
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}).WithTimeout(30 * time.Second).Should(Succeed())

	var secret corev1.Secret
	Expect(k8s.Get(ctx, types.NamespacedName{Namespace: "webshop", Name: "gitea-credentials"}, &secret)).To(Succeed())
	giteaToken = string(secret.Data["token"])
})

var _ = AfterSuite(func() {
	if forward != nil && forward.Process != nil {
		_ = forward.Process.Kill()
	}
	cancel()
})

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}
