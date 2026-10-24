/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/test/utils"
)

func key(ns, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}

func objMeta(ns, name string) metav1.ObjectMeta { return metav1.ObjectMeta{Namespace: ns, Name: name} }

func ref(name string) c4v1beta1.LocalRef { return c4v1beta1.LocalRef{Name: name} }

// newNamespace creates a namespace for one spec and deletes it afterwards.
func newNamespace(prefix string) string {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: prefix + "-"}}
	Expect(k8s.Create(ctx, ns)).To(Succeed())
	DeferCleanup(func() { _ = k8s.Delete(ctx, ns) })
	return ns.Name
}

func kubectl(args ...string) string {
	GinkgoHelper()
	out, err := utils.Run(exec.Command("kubectl", append([]string{"--context", kubeCtx}, args...)...))
	Expect(err).NotTo(HaveOccurred())
	return out
}

func copySecret(from, to, name string) {
	GinkgoHelper()
	var secret corev1.Secret
	Expect(k8s.Get(ctx, key(from, name), &secret)).To(Succeed())
	Expect(k8s.Create(ctx, &corev1.Secret{ObjectMeta: objMeta(to, name), Data: secret.Data})).To(Succeed())
}

func giteaRequest(method, path string) *http.Response {
	GinkgoHelper()
	req, err := http.NewRequestWithContext(ctx, method, giteaURL+path, nil)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "token "+giteaToken)
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	return resp
}

// giteaGet reads a Gitea API path into into. Right after a push the API can still
// report a repository as empty (409) for a moment, so it retries briefly.
func giteaGet(path string, into any) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		resp := giteaRequest(http.MethodGet, path)
		defer func() { _ = resp.Body.Close() }()
		g.Expect(resp.StatusCode).To(Equal(http.StatusOK), path)
		g.Expect(json.NewDecoder(resp.Body).Decode(into)).To(Succeed())
	}).WithTimeout(15 * time.Second).WithPolling(500 * time.Millisecond).Should(Succeed())
}

// giteaSend sends body as JSON to a Gitea API path and reads the reply into into.
func giteaSend(method, path string, body, into any) {
	GinkgoHelper()
	data, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred())
	req, err := http.NewRequestWithContext(ctx, method, giteaURL+path, bytes.NewReader(data))
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "token "+giteaToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	Expect(resp.StatusCode).To(BeNumerically("<", 300), "%s %s", method, path)
	Expect(json.NewDecoder(resp.Body).Decode(into)).To(Succeed())
}

func giteaDelete(path string) {
	resp := giteaRequest(http.MethodDelete, path)
	_ = resp.Body.Close()
}
