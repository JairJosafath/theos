/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8stypes "k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/features"
	"github.com/JairJosafath/c4factory/internal/adapters/argocd"
	githubdeploy "github.com/JairJosafath/c4factory/internal/adapters/github"
	"github.com/JairJosafath/c4factory/internal/adapters/gitlab"
	"github.com/JairJosafath/c4factory/internal/adapters/gitlab/gitlabtest"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/fake"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

// testStaleAfter is the Session Stale window in tests, much shorter than in production.
const testStaleAfter = 3 * time.Second

// testOperatorVersion and testChartVersion are what the Project reconciler
// reports in status.factory in tests.
const (
	testOperatorVersion = "1.2.3"
	testChartVersion    = "0.9.0"
	// testCIGoCache is the node root of the CI runner pools' Go caches, and
	// testRunnerRecordKept how long an ended pool pod stays readable, in tests.
	testCIGoCache        = "/var/cache/c4factory/go/ci"
	testRunnerRecordKept = 3 * time.Second
	// testRunnerBackoff is a pool's first wait after a pod failed, in tests.
	testRunnerBackoff = 500 * time.Millisecond
)

// testUIAccount is the UI's ServiceAccount, in the operator's namespace, that
// the Project reconciler lets read each project namespace's pods.
const testUIAccount = "c4-factory-ui"

// testScaffoldTimeout bounds a scaffold attempt in tests; the fake Git host answers at once.
const testScaffoldTimeout = 3 * time.Second

// testBotDeleteTimeout is how long a deleted Brief tries to delete its bot in tests.
const testBotDeleteTimeout = 5 * time.Second

var (
	// gitFake is the Git host every test scaffolds into.
	gitFake *fakeGit
	// githubFake is the GitHub every Crew publishes its releases to.
	githubFake *fakeGitHub
	// gitlabFake is the GitLab a gitlab DeployTarget ships to: a target's
	// url names it, and the adapter follows the url.
	gitlabFake *gitlabtest.Server
	// ghcrFake stands in for GHCR, where the github member copies a
	// release's image; registryFake is the owner's registry a spec pushes
	// that image to first (#1600). gitlabRegistryFake stands in for a
	// GitLab project's container registry (#1806).
	ghcrFake, registryFake, gitlabRegistryFake *fakeRegistry
	// deployFake is the Deployer of every DeployTarget's member, but for a
	// target whose repository the fake GitHub has (routedDeployer).
	deployFake *fake.Deployer
	// goProxyFake is the Go proxy every Crew asks about its released modules.
	goProxyFake *fakeGoProxy
	ctx         context.Context
	cancel      context.CancelFunc
	testEnv     *envtest.Environment
	cfg         *rest.Config
	k8sClient   client.Client
	// managerCfg is cfg as the operator's ServiceAccount, bound to the generated
	// role: a call the role doesn't grant fails the reconciler that makes it.
	managerCfg *rest.Config
	// managerClient is the manager's cached client, the reconcilers' view: a
	// spec waits on it when a reconcile must not see an object it deleted (#1300).
	managerClient client.Client
)

// The operator's namespace in the suite, and the Secrets it shares from there.
const (
	operatorNamespace = "c4-factory-system"
	sharedToken       = "shared-token"
	sharedPullSecret  = "shared-pull"
	// testModelProxyURL is the chart's real configuration: the model proxy's
	// Service in the release namespace (dist/chart/templates/model-proxy).
	testModelProxyURL = "http://c4-model-proxy.c4-factory-system.svc:8080"
)

// managerUser is the operator's ServiceAccount in config/rbac/role_binding.yaml.
const managerUser = "system:serviceaccount:system:controller-manager"

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	// Reconcilers run in the background, a chain of them per wait, and a loaded
	// device or CI runner has taken past 10s for one (#1099). A passing Eventually
	// returns as soon as it holds, so only a failing spec waits the full 30s.
	// Consistently keeps Gomega's own default; a spec gives its own duration.
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)

	// End the suite 30s before go test's -timeout (Makefile), so Ginkgo fails the
	// spec that is running by name and the slowest-specs report still runs,
	// instead of go test panicking with a goroutine dump.
	suiteConfig, reporterConfig := GinkgoConfiguration()
	if deadline, ok := t.Deadline(); ok {
		suiteConfig.Timeout = time.Until(deadline) - 30*time.Second
	}
	if s := os.Getenv("C4_TEST_SHARD"); s != "" {
		if _, err := fmt.Sscanf(s, "%d/%d", &shard.index, &shard.total); err != nil || shard.index < 1 || shard.index > shard.total {
			t.Fatalf("C4_TEST_SHARD=%q: want i/n, 1 <= i <= n", s)
		}
		if len(suiteConfig.FocusFiles) > 0 {
			t.Fatal("C4_TEST_SHARD focuses the shard's files; drop -ginkgo.focus-file, or C4_TEST_SHARD")
		}
		suiteConfig.FocusFiles, shard.specs = shardFocus(PreviewSpecs(suiteName), shard.index, shard.total)
	}
	RunSpecs(t, suiteName, suiteConfig, reporterConfig)
}

const suiteName = "Controller Suite"

// shard is this process's part of the suite. hack/go-test.sh runs the suite as
// C4_TEST_PROCS processes at once, each with its own control plane and
// manager, C4_TEST_SHARD=i/n naming which part (#1283); go test's cache
// answers each part on its own. Unset, the process runs the whole suite.
var shard struct {
	index, total int
	// specs is how many specs the shard runs; the suite fails when it ran
	// another number, so a spec no shard picked can't pass unseen.
	specs int
}

// shardFocus splits the specs the preview would run among n shards and
// returns the focus-file filters of shard i and how many specs it has. A
// top-level container is never split: Ginkgo runs those in random order, so
// they are independent of each other, while the specs inside one may share
// state (Ordered, a BeforeAll, a fixed name). The containers go to the
// shard with the fewest specs, largest first, so every shard gets the same
// split of the same suite.
func shardFocus(preview Report, i, n int) ([]string, int) {
	type group struct {
		location types.CodeLocation
		specs    int
	}
	var groups []*group
	byKey := map[string]*group{}
	for _, s := range preview.SpecReports {
		if s.LeafNodeType != types.NodeTypeIt || s.State != types.SpecStatePassed {
			continue // filtered out, pending, or not a spec
		}
		loc := s.LeafNodeLocation
		if len(s.ContainerHierarchyLocations) > 0 {
			loc = s.ContainerHierarchyLocations[0]
		}
		key := loc.String()
		if byKey[key] == nil {
			byKey[key] = &group{location: loc}
			groups = append(groups, byKey[key])
		}
		byKey[key].specs++
	}
	slices.SortFunc(groups, func(a, b *group) int {
		return cmp.Or(cmp.Compare(b.specs, a.specs), cmp.Compare(a.location.String(), b.location.String()))
	})
	load := make([]int, n)
	// A filter no spec matches, for a shard with no container.
	filters, specs := []string{"^$:0"}, 0
	for _, g := range groups {
		least := 0
		for j := range load {
			if load[j] < load[least] {
				least = j
			}
		}
		load[least] += g.specs
		if least == i-1 {
			filters = append(filters, fmt.Sprintf("^%s$:%d", regexp.QuoteMeta(g.location.FileName), g.location.LineNumber))
			specs += g.specs
		}
	}
	return filters, specs
}

var _ = ReportAfterSuite("the shard ran its specs", func(report Report) {
	if shard.total == 0 {
		return
	}
	Expect(report.PreRunStats.SpecsThatWillRun).To(Equal(shard.specs),
		"shard %d/%d ran other specs than its containers hold: a spec matched another shard's filter, or none",
		shard.index, shard.total)
})

// slowestSpecs is how many specs the report names.
const slowestSpecs = 10

// When C4_SLOWEST_SPECS names a file, the suite writes its slowest specs there,
// so the Tests job's log names what made a slow run slow (go test shows no
// output of a package that passes). A shard writes FILE.i, which
// hack/go-test.sh merges into FILE.
var _ = ReportAfterSuite("slowest specs", func(report Report) {
	path := os.Getenv("C4_SLOWEST_SPECS")
	if path == "" {
		return
	}
	if shard.total > 0 {
		path = fmt.Sprintf("%s.%d", path, shard.index)
	}
	var specs []SpecReport
	for _, s := range report.SpecReports {
		if s.LeafNodeType == types.NodeTypeIt && s.State != types.SpecStateSkipped {
			specs = append(specs, s)
		}
	}
	slices.SortFunc(specs, func(a, b SpecReport) int { return cmp.Compare(b.RunTime, a.RunTime) })
	var out bytes.Buffer
	fmt.Fprintf(&out, suiteName+": %d specs in %.0fs; the %d slowest:\n",
		len(specs), report.RunTime.Seconds(), min(slowestSpecs, len(specs)))
	for _, s := range specs[:min(slowestSpecs, len(specs))] {
		fmt.Fprintf(&out, "%6.1fs  %s  (%s)\n", s.RunTime.Seconds(), s.FullText(), s.LeafNodeLocation)
	}
	Expect(os.WriteFile(path, out.Bytes(), 0o644)).To(Succeed())
})

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = c4v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	// With v1beta1 every kind is convertible, so envtest points every CRD's
	// conversion at the manager's webhook server (#1615).
	err = c4v1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	// The webhook certificate patches the CRDs (#1614).
	err = apiextensionsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "test", "crds", "agent-sandbox"),
			filepath.Join("..", "..", "test", "crds", "argocd"),
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

	By("making the operator's namespace, the source of shared Secrets")
	Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: operatorNamespace}})).To(Succeed())

	By("starting the manager with every reconciler, as that ServiceAccount")
	webhookOptions := &testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(managerCfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		WebhookServer: webhook.NewServer(webhook.Options{Host: webhookOptions.LocalServingHost,
			Port: webhookOptions.LocalServingPort, CertDir: webhookOptions.LocalServingCertDir}),
	})
	Expect(err).NotTo(HaveOccurred())
	// As cmd/main.go does when the webhook server runs.
	mgr.GetWebhookServer().Register("/convert",
		conversion.NewWebhookHandler(mgr.GetScheme(), mgr.GetConverterRegistry()))
	managerClient = mgr.GetClient()
	gitFake, err = newFakeGit(filepath.Join("..", "..", "templates"))
	Expect(err).NotTo(HaveOccurred())
	githubFake = newFakeGitHub()
	DeferCleanup(githubFake.server.Close)
	DeferCleanup(githubFake.enterprise.Close)
	gitlabFake = gitlabtest.StartTLS()
	DeferCleanup(gitlabFake.Close)
	ghcrFake, registryFake, gitlabRegistryFake = newFakeRegistry(), newFakeRegistry(), newFakeRegistry()
	DeferCleanup(ghcrFake.server.Close)
	DeferCleanup(registryFake.server.Close)
	DeferCleanup(gitlabRegistryFake.server.Close)
	deployFake = &fake.Deployer{}
	goProxyFake = newFakeGoProxy()
	DeferCleanup(goProxyFake.server.Close)
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
	// The Argo CD adapter's client, uncached as in cmd/main.go, under the
	// generated role too.
	argoClient, err := client.New(managerCfg, client.Options{Scheme: s, Mapper: mgr.GetRESTMapper(),
		HTTPClient: mgr.GetHTTPClient()})
	if err != nil {
		return err
	}
	deployers := ports.Deployers{model.DeployMemberGitHub: routedDeployer{},
		// The gitlab adapter follows the target's url, so a target on
		// gitlabFake's URL ships to the fake.
		model.DeployMemberGitLab: gitlab.New(gitlabProjects{}),
		model.DeployMemberArgoCD: argocd.New(argoClient),
		// The cloudFormation member's adapter is tested on its fake AWS in
		// internal/adapters/cloudformation; the suite stands deployFake in.
		model.DeployMemberCloudFormation: deployFake}
	for _, r := range []interface{ SetupWithManager(ctrl.Manager) error }{
		&ProjectReconciler{Client: c, Scheme: s, APIReader: mgr.GetAPIReader(),
			SharedSecrets: []string{sharedToken}, SharedPullSecrets: []string{sharedPullSecret},
			SourceNamespace: operatorNamespace, SharedSecretsRecheck: time.Second,
			ModelProxyURL:   testModelProxyURL,
			OperatorVersion: testOperatorVersion, ChartVersion: testChartVersion,
			UIServiceAccount: k8stypes.NamespacedName{Namespace: operatorNamespace, Name: testUIAccount},
			Git:              gitFake, CIGoCache: testCIGoCache, RunnerRecordKept: testRunnerRecordKept,
			RunnerBackoff: testRunnerBackoff},
		&SoftwareSystemReconciler{Client: c, Scheme: s},
		&ContainerReconciler{Client: crewListFaults.wrap(c), Scheme: s, APIReader: mgr.GetAPIReader(), Git: gitFake, ChecksRecheck: time.Second, SecretRecheck: time.Second,
			ScaffoldOptions: sessionFeature, ScaffoldTimeout: testScaffoldTimeout, Recorder: mgr.GetEventRecorder("container"),
			Deployers: deployers, DeployMembers: testDeployMember,
			DeployPoll: time.Second},
		&DataStoreReconciler{Client: c, Scheme: s},
		&RelationshipReconciler{Client: c, Scheme: s},
		&SessionReconciler{Client: c, Scheme: s, StaleAfter: testStaleAfter},
		&TaskReconciler{Client: taskStatusFaults.wrap(c), Scheme: s, APIReader: mgr.GetAPIReader(), Git: gitFake, ChecksRecheck: time.Second, SecretRecheck: time.Second,
			StandardChangeRecheck: time.Second, PullRequestRecheck: time.Second, Recorder: mgr.GetEventRecorder("task"),
			SharedSecrets: []string{sharedToken}, SourceNamespace: operatorNamespace, ModelProxyURL: testModelProxyURL,
			Now: taskNow},
		&BriefReconciler{Client: planBriefs.wrap(c), Scheme: s, APIReader: mgr.GetAPIReader(), Git: gitFake,
			Recorder: mgr.GetEventRecorder("brief"), NeedsInputRecheck: time.Second, SuggestionsRecheck: time.Second, PlanCheckTimeout: 3 * time.Second,
			BotDeleteTimeout: testBotDeleteTimeout, SharedSecrets: []string{sharedToken}, SourceNamespace: operatorNamespace,
			ModelProxyURL: testModelProxyURL,
			Issues:        githubFake, Now: briefNow},
		&CrewReconciler{Client: c, Scheme: s, APIReader: secretReader{mgr.GetAPIReader()}, Git: gitFake, PollInterval: time.Second,
			Recorder: mgr.GetEventRecorder("crew"), Publish: githubFake, Now: crewNow,
			GoProxy:   goProxyFake.server.URL,
			Deployers: ports.Deployers{model.DeployMemberGitHub: githubdeploy.New(githubFake)}},
		&DeployTargetReconciler{Client: c, Scheme: s, APIReader: mgr.GetAPIReader(), PollInterval: time.Second,
			Deployers: deployers},
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

// gitlabProjects is the real GitLab client, trusting gitlabFake's certificate.
type gitlabProjects struct{}

func (gitlabProjects) GitLab(cfg gitprovider.GitLabConfig) (gitprovider.GitLabProject, error) {
	return gitprovider.Client{HTTP: gitlabFake.Client()}.GitLab(cfg)
}

// routedDeployer is the github member's Deployer in the suite: the github
// adapter against the fake GitHub for a target whose repository the fake
// has, so a spec can watch what a deploy makes there, else deployFake,
// whose state a spec sets by hand.
type routedDeployer struct{}

func (routedDeployer) of(t ports.Target) ports.Deployer {
	if githubFake.hasRepository(t.Settings[model.GitHubSettingRepository]) {
		return githubdeploy.New(githubFake, githubdeploy.WithImageRegistry(ghcrFake.host))
	}
	return deployFake
}

func (r routedDeployer) Check(ctx context.Context, t ports.Target) error {
	return r.of(t).Check(ctx, t)
}

func (r routedDeployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	return r.of(t).Ship(ctx, t, s)
}

func (r routedDeployer) State(ctx context.Context, t ports.Target, app, version string) (ports.DeployState, error) {
	return r.of(t).State(ctx, t, app, version)
}
