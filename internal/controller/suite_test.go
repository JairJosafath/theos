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

package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/features"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

// testStaleAfter is the Session Stale window in tests, much shorter than in production.
const testStaleAfter = 3 * time.Second

// testScaffoldTimeout bounds a scaffold attempt in tests; the fake Git host answers at once.
const testScaffoldTimeout = 3 * time.Second

var (
	// gitFake is the Git host every test scaffolds into.
	gitFake   *fakeGit
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	// Reconcilers run in the background; give them time on a busy machine or CI runner.
	SetDefaultEventuallyTimeout(10 * time.Second)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = c4v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "test", "crds", "agent-sandbox"),
		},
		ErrorIfCRDPathMissing: true,
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	By("installing the admission policy")
	Expect(applyManifests(ctx, filepath.Join("..", "..", "config", "policy", "project_name_policy.yaml"))).To(Succeed())

	By("starting the manager with every reconciler")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())
	gitFake, err = newFakeGit(filepath.Join("..", "..", "templates"))
	Expect(err).NotTo(HaveOccurred())
	Expect(SetupIndexes(ctx, mgr)).To(Succeed())
	Expect(setupReconcilers(mgr)).To(Succeed())
	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

// setupReconcilers registers every reconciler the way cmd/main.go does.
func setupReconcilers(mgr ctrl.Manager) error {
	c, s := mgr.GetClient(), mgr.GetScheme()
	sessionFeature, err := features.ScaffoldOptions(features.SessionFeatureLocal, "")
	if err != nil {
		return err
	}
	for _, r := range []interface{ SetupWithManager(ctrl.Manager) error }{
		&ProjectReconciler{Client: c, Scheme: s},
		&SoftwareSystemReconciler{Client: c, Scheme: s},
		&ContainerReconciler{Client: c, Scheme: s, APIReader: mgr.GetAPIReader(), Git: gitFake,
			ScaffoldOptions: sessionFeature, ScaffoldTimeout: testScaffoldTimeout},
		&DataStoreReconciler{Client: c, Scheme: s},
		&RelationshipReconciler{Client: c, Scheme: s},
		&SessionReconciler{Client: c, Scheme: s, StaleAfter: testStaleAfter},
		&TaskReconciler{Client: c, Scheme: s, APIReader: mgr.GetAPIReader(), Git: gitFake, ChecksRecheck: time.Second},
	} {
		if err := r.SetupWithManager(mgr); err != nil {
			return err
		}
	}
	return nil
}

// applyManifests creates every object in a multi-document YAML file.
func applyManifests(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var obj unstructured.Unstructured
		if err := decoder.Decode(&obj.Object); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if len(obj.Object) == 0 {
			continue
		}
		if err := k8sClient.Create(ctx, &obj); err != nil {
			return err
		}
	}
}

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// Makefile targets, the 'BinaryAssetsDirectory' must be explicitly configured.
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'make setup-envtest' beforehand.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
