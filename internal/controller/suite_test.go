/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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
	gitFake *fakeGit
	// githubFake is the GitHub every Crew publishes its releases to.
	githubFake *fakeGitHub
	ctx        context.Context
	cancel     context.CancelFunc
	testEnv    *envtest.Environment
	cfg        *rest.Config
	k8sClient  client.Client
	// managerCfg is cfg as the operator's ServiceAccount, bound to the generated
	// role: a call the role doesn't grant fails the reconciler that makes it.
	managerCfg *rest.Config
)

// managerUser is the operator's ServiceAccount in config/rbac/role_binding.yaml.
const managerUser = "system:serviceaccount:system:controller-manager"

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

	By("binding the generated role to the operator's ServiceAccount")
	for _, file := range []string{"role.yaml", "role_binding.yaml"} {
		Expect(applyManifests(ctx, filepath.Join("..", "..", "config", "rbac", file))).To(Succeed())
	}
	managerCfg = rest.CopyConfig(cfg)
	managerCfg.Impersonate = rest.ImpersonationConfig{UserName: managerUser}

	By("starting the manager with every reconciler, as that ServiceAccount")
	mgr, err := ctrl.NewManager(managerCfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())
	gitFake, err = newFakeGit(filepath.Join("..", "..", "templates"))
	Expect(err).NotTo(HaveOccurred())
	githubFake = newFakeGitHub()
	DeferCleanup(githubFake.server.Close)
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
		&ContainerReconciler{Client: c, Scheme: s, APIReader: mgr.GetAPIReader(), Git: gitFake, ChecksRecheck: time.Second,
			ScaffoldOptions: sessionFeature, ScaffoldTimeout: testScaffoldTimeout},
		&DataStoreReconciler{Client: c, Scheme: s},
		&RelationshipReconciler{Client: c, Scheme: s},
		&SessionReconciler{Client: c, Scheme: s, StaleAfter: testStaleAfter},
		&TaskReconciler{Client: c, Scheme: s, APIReader: mgr.GetAPIReader(), Git: gitFake, ChecksRecheck: time.Second,
			StandardChangeRecheck: time.Second},
		&BriefReconciler{Client: c, Scheme: s, APIReader: mgr.GetAPIReader(), Git: gitFake,
			Recorder: mgr.GetEventRecorder("brief"), NeedsInputRecheck: time.Second, SuggestionsRecheck: time.Second, PlanCheckTimeout: 3 * time.Second,
			BotDeleteTimeout: 5 * time.Second},
		&CrewReconciler{Client: c, Scheme: s, APIReader: secretReader{mgr.GetAPIReader()}, Git: gitFake, PollInterval: time.Second,
			Recorder: mgr.GetEventRecorder("crew"), Publish: githubFake},
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
	// make setup-envtest installs them in the main checkout's .dev/, which a
	// worktree links to (./dev setup).
	basePath := filepath.Join("..", "..", ".dev", "make", "bin", "k8s")
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
