/*
Copyright 2026 The theos Authors.

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

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"os"
	"slices"
	"strings"
	"time"

	// The time zones of release cadences load whatever the image holds
	// (docs/plans/done/release-cadence.md, C1).
	_ "time/tzdata"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/features"
	"github.com/JairJosafath/theos/internal/adapters/argocd"
	"github.com/JairJosafath/theos/internal/adapters/awsenv"
	"github.com/JairJosafath/theos/internal/adapters/cloudformation"
	"github.com/JairJosafath/theos/internal/adapters/git"
	"github.com/JairJosafath/theos/internal/adapters/gitea"
	"github.com/JairJosafath/theos/internal/adapters/github"
	"github.com/JairJosafath/theos/internal/adapters/gitlab"
	"github.com/JairJosafath/theos/internal/adapters/jira"
	"github.com/JairJosafath/theos/internal/adapters/linear"
	"github.com/JairJosafath/theos/internal/adapters/localenv"
	"github.com/JairJosafath/theos/internal/adapters/matrix"
	"github.com/JairJosafath/theos/internal/buildinfo"
	"github.com/JairJosafath/theos/internal/controller"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	// The webhook certificate's CA is patched into the CRDs.
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(c4v1beta1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var webhookPort int
	var webhookService, webhookCertSecret string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var sessionStaleAfter time.Duration
	var scaffoldSettle time.Duration
	var maxAgentSandboxes int
	var leaseDuration time.Duration
	var workspaceMemory string
	var checkpointStorageClass string
	var sandboxGoCache string
	var ciGoCache string
	var goProxy string
	var clusterDNS stringList
	var sessionFeature, sessionBinaryURL string
	var sharedSecrets, sharedPullSecrets stringList
	var uiServiceAccount string
	var modelProxyURL string
	var chatHomeserver string
	var linearAPIURL, jiraAPIURL string
	var gitEventsURL string
	var gitEventsAddr string
	var alertDrainTimeout time.Duration
	var testEnvToolsImage, testEnvToolsURL, testEnvRunnerURL string
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "Port the webhook server listens on. "+
		"Defaults to 9443. Set -1 to disable the webhook server.")
	flag.StringVar(&webhookService, "webhook-service", "",
		"The manager's webhook Service in the operator's namespace (POD_NAMESPACE): the operator issues the webhook "+
			"certificate for <service>.<namespace>.svc into --webhook-cert-secret and patches its CA into the "+
			"theos.factory.io CRDs that convert by webhook. None: no webhook server, unless --webhook-cert-path names "+
			"certificates.")
	flag.StringVar(&webhookCertSecret, "webhook-cert-secret", "",
		"The Secret in the operator's namespace the webhook certificate is kept in; needs --webhook-service.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	opts := zap.Options{
		Development: true,
	}
	flag.IntVar(&maxAgentSandboxes, "max-agent-sandboxes", 1,
		"How many agent sandboxes may run at once, across all projects; 0 means no limit. "+
			"Each runs an agent and its checks, which is heavy on a small cluster.")
	flag.StringVar(&workspaceMemory, "sandbox-workspace-memory", "",
		"When set, such as 3Gi, each agent sandbox's workspace is in memory, up to this size, instead of "+
			"on the node's disk; it counts against the Agent's memory limit. On a small cluster, checks "+
			"writing to the disk etcd uses stall the API server.")
	flag.StringVar(&checkpointStorageClass, "sandbox-checkpoint-storage-class", "",
		"The StorageClass of each agent sandbox's checkpoint volume, where an interrupted run is saved "+
			"to resume in place; empty means the cluster's default class. "+controller.NoCheckpointClass+
			" gives sandboxes no checkpoint volume, for a cluster without a class to provision one: an "+
			"interrupted run then runs again.")
	flag.StringVar(&sandboxGoCache, "sandbox-go-cache", "",
		"When set, a directory on the node, such as /var/cache/theos/go/sandbox, that agent sandboxes "+
			"share as their Go and golangci-lint build cache, so a round doesn't compile what an earlier one did. "+
			"Never the CI runner's cache: agents run untrusted code, and Go trusts its cache.")
	flag.StringVar(&ciGoCache, "ci-go-cache", "",
		"When set, a directory on the node, such as /var/cache/theos/go/ci, under which each project's "+
			"CI runner pods get their own Go and golangci-lint build cache, <dir>/<project namespace>; a job "+
			"compiles only what changed. Never shared with the agent sandboxes' cache.")
	flag.Var(&clusterDNS, "cluster-dns",
		"Where cluster DNS runs, which the agent and CI fences let port 53 out to: namespace/key=value[,key=value] "+
			"(a namespace and pod labels) or an address block inside the private ranges (169.254.20.10/32 for "+
			"NodeLocal DNSCache; a bare address means /32). Repeatable. Default "+controller.DefaultClusterDNS+".")
	flag.StringVar(&goProxy, "go-proxy", controller.DefaultGoProxy,
		"The Go proxy asked whether go get finds a released Go module whose path is outside theos.local/; "+
			"off asks none, and the Crew's own GitHub source tag is the answer.")
	flag.DurationVar(&leaseDuration, "leader-elect-lease-duration", time.Minute,
		"How long the leader's lease lasts. It renews within two thirds of that, retrying every twelfth. "+
			"The client default of 15s ends the manager whenever the API server stalls for 10s, which a "+
			"busy single-node cluster does while agents and CI run.")
	flag.DurationVar(&scaffoldSettle, "scaffold-settle", model.DefaultScaffoldSettle,
		"How long a new Container waits after its creation before it is scaffolded, so the Relationships "+
			"applied with it are there; 0 scaffolds at once (#43).")
	flag.DurationVar(&sessionStaleAfter, "session-stale-after", model.DefaultStaleAfter,
		"How long a Session may go without a heartbeat before it is marked Stale.")
	flag.StringVar(&sessionFeature, "session-feature", features.SessionFeatureLocal,
		"How scaffolded devcontainers get the Session client: local (write the feature into each repository), "+
			"none, or a feature reference such as an OCI image.")
	flag.StringVar(&sessionBinaryURL, "session-binary-url", "",
		"URL of a linux theos-session binary for the feature to download; ${arch} becomes amd64 or arm64.")
	flag.Var(&sharedSecrets, "shared-secret",
		"The Agents' token Secret in the operator's namespace (POD_NAMESPACE), such as claude-credentials, read "+
			"by the model proxy and never copied: an Agent in a project namespace labeled "+
			"theos.factory.io/shared-secrets=true whose credentialsSecretRef names it is on the factory's shared "+
			"token; repeatable, needs --model-proxy-url. None: every Agent's Secret is its project's own.")
	flag.Var(&sharedPullSecrets, "shared-pull-secret",
		"An image pull Secret in the operator's namespace to copy into every project namespace labeled "+
			"theos.factory.io/shared-secrets=true; repeatable. None: no pull Secret is shared.")
	flag.StringVar(&modelProxyURL, "model-proxy-url", "",
		"The model proxy's URL, such as http://c4-model-proxy.<namespace>.svc:8080: an agent sandbox whose Agent "+
			"names a --shared-secret Secret holds no token and reaches Claude through it. None: no shared token.")
	flag.StringVar(&uiServiceAccount, "ui-service-account", "",
		"The UI's ServiceAccount, as namespace/name, let read the pods and their metrics in every project "+
			"namespace for the Factory view's health. None: the UI sees no agent sandboxes.")
	flag.StringVar(&chatHomeserver, "chat-homeserver", "",
		"The Matrix homeserver of a Chat that names none, such as the chart's in-cluster one. None: each Chat "+
			"names its homeserver.")
	flag.StringVar(&linearAPIURL, "linear-api-url", linear.APIURL,
		"Linear's GraphQL address, which a Brief's request source on Linear is read and answered at.")
	flag.StringVar(&jiraAPIURL, "jira-api-url", jira.APIURL,
		"Atlassian's API gateway, which a Brief's request source on Jira Cloud is read and answered at, "+
			"with the site's cloud ID: <url>/ex/jira/<cloudID>.")
	flag.StringVar(&gitEventsURL, "git-events-url", "",
		"The base address the Git host delivers events to, such as http://theos-git-events.<namespace>.svc:8082: "+
			"each GitProvider's hook delivers to <url>/git-events/<namespace>/<name>. None: no hook, and the "+
			"operator polls the host.")
	flag.StringVar(&gitEventsAddr, "git-events-bind-address", "0",
		"The address the receiver of the Git host's deliveries binds to, such as :8082, on the leader. "+
			"Leave as 0 to receive none: the operator polls the host.")
	flag.DurationVar(&alertDrainTimeout, "alert-drain-timeout", controller.DefaultAlertDrainTimeout,
		"How long the operator, stopped by a signal, files the alerts it took and has not filed yet. It has to "+
			"fit the pod's termination grace period: past it the kill cuts the drain, and what the inbox held "+
			"then is lost unsaid.")
	flag.StringVar(&testEnvToolsImage, "test-environment-tools-image", "",
		"The image holding theos-env at /usr/local/bin/theos-env, such as the release's theos-agent image: a "+
			"local TestEnvironment's lease pods copy it in. None: no local environment can be leased.")
	flag.StringVar(&testEnvToolsURL, "test-environment-tools-url", "",
		"Where a remote TestEnvironment's machines download theos-env, an https:// address in which ${os} "+
			"becomes linux or darwin and ${arch} amd64 or arm64, with a SHA256SUMS next to it, such as the "+
			"release's theos-env-${os}-${arch}. "+
			"None: no remote environment can be leased.")
	flag.StringVar(&testEnvRunnerURL, "test-environment-runner-url", "",
		"Where a remote TestEnvironment's machine that runs a CI job downloads the CI runner, an https:// address "+
			"in which ${arch} becomes amd64 or arm64, with a SHA256SUMS next to it, such as the CI runner the "+
			"release attaches beside theos-env. None: no remote environment runs CI jobs.")
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	clusterDNSPeers, err := controller.ClusterDNSPeers(clusterDNS)
	if err != nil {
		setupLog.Error(err, "Invalid --cluster-dns")
		os.Exit(1)
	}

	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" && len(sharedSecrets)+len(sharedPullSecrets) > 0 {
		setupLog.Error(nil, "--shared-secret and --shared-pull-secret need POD_NAMESPACE, the operator's namespace")
		os.Exit(1)
	}
	if (webhookService != "" || webhookCertSecret != "") &&
		(podNamespace == "" || webhookService == "" || webhookCertSecret == "" || webhookCertPath != "") {
		setupLog.Error(nil, "--webhook-service and --webhook-cert-secret go together, need POD_NAMESPACE "+
			"and exclude --webhook-cert-path")
		os.Exit(1)
	}
	if podNamespace == "" && modelProxyURL != "" {
		setupLog.Error(nil, "--model-proxy-url needs POD_NAMESPACE, the operator's namespace")
		os.Exit(1)
	}
	if len(sharedSecrets) > 0 && modelProxyURL == "" {
		setupLog.Error(nil, "--shared-secret needs --model-proxy-url: the token is never copied into a project "+
			"namespace, so a sandbox reaches it only through the model proxy")
		os.Exit(1)
	}

	var workspaceQuantity *resource.Quantity
	if workspaceMemory != "" {
		q, err := resource.ParseQuantity(workspaceMemory)
		if err != nil {
			setupLog.Error(err, "Invalid --sandbox-workspace-memory", "value", workspaceMemory)
			os.Exit(1)
		}
		workspaceQuantity = &q
	}

	var uiAccount types.NamespacedName
	if uiServiceAccount != "" {
		ns, name, ok := strings.Cut(uiServiceAccount, "/")
		if !ok || ns == "" || name == "" {
			setupLog.Error(nil, "Invalid --ui-service-account: want namespace/name", "value", uiServiceAccount)
			os.Exit(1)
		}
		uiAccount = types.NamespacedName{Namespace: ns, Name: name}
	}

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
		Port:    webhookPort,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	// The operator issues the webhook certificate itself (docs/plans/done/api-versions.md,
	// decision 3): read or made before the manager starts, so the server
	// starts with it, and checked again daily.
	var webhookCert *controller.WebhookCert
	if webhookService != "" {
		direct, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
		if err != nil {
			setupLog.Error(err, "Failed to create the webhook certificate's client")
			os.Exit(1)
		}
		webhookCert = &controller.WebhookCert{Client: direct, Namespace: podNamespace,
			Secret: webhookCertSecret, Service: webhookService}
		if err := webhookCert.Ensure(ctrl.LoggerInto(context.Background(), setupLog)); err != nil {
			setupLog.Error(err, "Failed to set up the webhook certificate")
			os.Exit(1)
		}
		webhookServerOptions.TLSOpts = append(slices.Clone(webhookTLSOpts), func(c *tls.Config) {
			c.GetCertificate = webhookCert.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	renewDeadline, retryPeriod := leaseDuration*2/3, leaseDuration/12
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "81ad068e.factory.io",
		LeaseDuration:          &leaseDuration,
		RenewDeadline:          &renewDeadline,
		RetryPeriod:            &retryPeriod,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	if err := controller.SetupIndexes(context.Background(), mgr); err != nil {
		setupLog.Error(err, "Failed to set up field indexes")
		os.Exit(1)
	}
	// The receiver of the Git host's deliveries, nil when off; the Container,
	// Crew, Task, Brief and GitProvider controllers each read its Events
	// (docs/plans/done/git-events.md, decisions 5 to 8).
	var gitEvents *controller.GitEventsReceiver
	if gitEventsAddr != "0" && gitEventsAddr != "" {
		gitEvents = &controller.GitEventsReceiver{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
			Git: gitClient{}, BindAddress: gitEventsAddr}
		if err := mgr.Add(gitEvents); err != nil {
			setupLog.Error(err, "Failed to add the Git events receiver")
			os.Exit(1)
		}
	}
	// The receiver of alerts, on the same listener; nil when it is off
	// (docs/plans/alerts.md, decision 2). Only a signal ends signalCtx, so
	// the receiver drains on SIGTERM and not when leadership is lost.
	signalCtx := ctrl.SetupSignalHandler()
	var alerts *controller.AlertReceiver
	if gitEvents != nil {
		alerts = &controller.AlertReceiver{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
			SignalContext: signalCtx, DrainTimeout: alertDrainTimeout}
		gitEvents.Alerts = alerts
		if err := mgr.Add(alerts); err != nil {
			setupLog.Error(err, "Failed to add the alert receiver")
			os.Exit(1)
		}
	}

	if err := (&controller.ProjectReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		APIReader:         mgr.GetAPIReader(),
		SharedSecrets:     sharedSecrets,
		SharedPullSecrets: sharedPullSecrets,
		SourceNamespace:   podNamespace,
		ModelProxyURL:     modelProxyURL,
		OperatorVersion:   buildinfo.Version,
		ChartVersion:      os.Getenv("THEOS_CHART_VERSION"),
		UIServiceAccount:  uiAccount,
		Git:               gitClient{},
		CIGoCache:         ciGoCache,
		ClusterDNS:        clusterDNSPeers,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "project")
		os.Exit(1)
	}
	if err := (&controller.SoftwareSystemReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "softwaresystem")
		os.Exit(1)
	}
	scaffoldOptions, err := features.ScaffoldOptions(sessionFeature, sessionBinaryURL)
	if err != nil {
		setupLog.Error(err, "Failed to load the Session feature")
		os.Exit(1)
	}
	// Each DeployTarget member's adapter, by the member's name. The Argo CD
	// adapter reads uncached, so a check on a cluster without Argo CD starts
	// no informer on a CRD that isn't there.
	argoClient, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme(),
		Mapper: mgr.GetRESTMapper(), HTTPClient: mgr.GetHTTPClient()})
	if err != nil {
		setupLog.Error(err, "Failed to make the Argo CD adapter's client")
		os.Exit(1)
	}
	deployers := ports.Deployers{model.DeployMemberGitHub: github.New(github.Client{}),
		model.DeployMemberGitLab:         gitlab.New(nil),
		model.DeployMemberArgoCD:         argocd.New(argoClient),
		model.DeployMemberCloudFormation: cloudformation.New(nil)}
	// Each TestEnvironment member's provider, by the member's name.
	testEnvProviders := ports.TestEnvironmentProviders{
		model.TestEnvMemberLocal:      localenv.New(mgr.GetClient(), testEnvToolsImage),
		model.TestEnvMemberAWS:        awsenv.New(nil, testEnvToolsURL).WithRunnerURL(testEnvRunnerURL),
		model.TestEnvMemberDeviceFarm: awsenv.NewDeviceFarm(nil)}
	if err := (&controller.ContainerReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		APIReader:       mgr.GetAPIReader(),
		Git:             gitClient{},
		ScaffoldOptions: scaffoldOptions,
		ScaffoldSettle:  func() time.Duration { return scaffoldSettle },
		Recorder:        mgr.GetEventRecorder("container"),
		GitEvents:       gitEvents,
		Deployers:       deployers,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "container")
		os.Exit(1)
	}
	if err := (&controller.DataStoreReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "datastore")
		os.Exit(1)
	}
	if err := (&controller.RelationshipReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "relationship")
		os.Exit(1)
	}
	if err := (&controller.SessionReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		StaleAfter: sessionStaleAfter,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "session")
		os.Exit(1)
	}
	if err := (&controller.TaskReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		APIReader:         mgr.GetAPIReader(),
		Git:               gitClient{},
		MaxSandboxes:      maxAgentSandboxes,
		WorkspaceMemory:   workspaceQuantity,
		CheckpointClass:   checkpointStorageClass,
		SandboxGoCache:    sandboxGoCache,
		SharedPullSecrets: sharedPullSecrets,
		SharedSecrets:     sharedSecrets,
		SourceNamespace:   podNamespace,
		ModelProxyURL:     modelProxyURL,
		Recorder:          mgr.GetEventRecorder("task"),
		GitEvents:         gitEvents,
		TestEnvironments:  testEnvProviders,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "task")
		os.Exit(1)
	}
	if err := (&controller.CrewReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		APIReader: mgr.GetAPIReader(),
		Deployers: deployers,
		Git:       gitClient{},
		Publish:   github.Client{},
		Recorder:  mgr.GetEventRecorder("crew"),
		GitEvents: gitEvents,
		GoProxy:   goProxyURL(goProxy),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "crew")
		os.Exit(1)
	}
	// A Brief reads requests from its releases repositories' GitHub issues
	// and from its request sources on Linear and Jira Cloud.
	trackers := ports.IssueTrackers{model.TrackerGitHub: github.Client{},
		model.TrackerLinear: linear.Client{APIURL: linearAPIURL}, model.TrackerJira: jira.Client{Address: jiraAPIURL}}
	if err := (&controller.BriefReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		APIReader:       mgr.GetAPIReader(),
		Git:             gitClient{},
		Issues:          trackers,
		Recorder:        mgr.GetEventRecorder("brief"),
		GitEvents:       gitEvents,
		SharedSecrets:   sharedSecrets,
		SourceNamespace: podNamespace,
		ModelProxyURL:   modelProxyURL,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "brief")
		os.Exit(1)
	}
	if err := (&controller.DeployTargetReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		APIReader: mgr.GetAPIReader(),
		Deployers: deployers,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "deploytarget")
		os.Exit(1)
	}
	// One chat adapter per chat host, by the name of its model.ChatSystem row.
	if err := (&controller.ChatReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		APIReader:  mgr.GetAPIReader(),
		ChatHosts:  map[string]ports.ChatHosts{model.ChatMatrix: matrix.Hosts(nil)},
		Homeserver: chatHomeserver,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "chat")
		os.Exit(1)
	}
	if err := (&controller.ScorerReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		APIReader: mgr.GetAPIReader(),
		Git:       gitClient{},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "scorer")
		os.Exit(1)
	}
	if err := (&controller.BenchmarkReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		APIReader: mgr.GetAPIReader(),
		Git:       gitClient{},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "benchmark")
		os.Exit(1)
	}
	if err := (&controller.GitProviderReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		APIReader:    mgr.GetAPIReader(),
		Git:          gitClient{},
		GitEventsURL: gitEventsURL,
		GitEvents:    gitEvents,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "gitprovider")
		os.Exit(1)
	}
	if err := (&controller.TestEnvironmentReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		APIReader:         mgr.GetAPIReader(),
		Providers:         testEnvProviders,
		Git:               gitClient{},
		SharedPullSecrets: sharedPullSecrets,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "testenvironment")
		os.Exit(1)
	}
	alertSources := &controller.AlertSourceReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		APIReader: mgr.GetAPIReader(),
		Git:       gitClient{},
		Receiver:  alerts,
		EventsURL: gitEventsURL,
	}
	if alerts != nil {
		alerts.Drainer = alertSources
	}
	if err := alertSources.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "alertsource")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	webhookOn := webhookCert != nil || webhookCertPath != ""
	if webhookOn {
		// A theos.factory.io kind served at more than one version converts
		// here: each older version's type is Convertible to the storage
		// version's, its hub (docs/plans/done/api-versions.md, decision 3).
		// Every kind serves v1beta1 alone (#1963), so no CRD points here yet;
		// the next version adds its types to the scheme above.
		mgr.GetWebhookServer().Register("/convert",
			conversion.NewWebhookHandler(mgr.GetScheme(), mgr.GetConverterRegistry()))
	}
	// Whatever the webhook setting, the leader rewrites what etcd still holds
	// at a version other than the storage one and brings each CRD's
	// storedVersions down (docs/plans/done/api-versions.md, decision 4); a start
	// after the pass does nothing. A kind at strategy None converts in the
	// API server; one that converts by this pod's webhook waits until it
	// serves (#1884). The Service may still route a conversion to an older
	// pod during a rollout; the migrator's retry covers that.
	migrator := &controller.StorageMigrator{}
	if webhookOn {
		migrator.Ready = mgr.GetWebhookServer().StartedChecker()
	}
	if migrator.Client, err = client.New(mgr.GetConfig(), client.Options{Scheme: scheme}); err != nil {
		setupLog.Error(err, "Failed to create the storage migrator's client")
		os.Exit(1)
	}
	if err := mgr.Add(migrator); err != nil {
		setupLog.Error(err, "Failed to add the storage migrator")
		os.Exit(1)
	}
	if webhookCert != nil {
		// The webhook server starts before the caches; the pod is ready, and in
		// the Service, once it serves.
		if err := mgr.Add(webhookCert); err != nil {
			setupLog.Error(err, "Failed to add the webhook certificate's recheck")
			os.Exit(1)
		}
		if err := mgr.AddReadyzCheck("webhook", mgr.GetWebhookServer().StartedChecker()); err != nil {
			setupLog.Error(err, "Failed to set up the webhook ready check")
			os.Exit(1)
		}
	}
	// The factory's numbers on /metrics, computed from the cached Tasks at
	// each scrape (#59).
	if err := ctrlmetrics.Registry.Register(controller.FactoryMetrics{Reader: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "Failed to register the factory's metrics")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager", "version", buildinfo.Version, "chartVersion", os.Getenv("THEOS_CHART_VERSION"))
	if err := mgr.Start(signalCtx); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

// gitClient is the controllers' GitClient: the Gitea adapter builds the Git
// host's Providers, and plain Git fetches templates.
type gitClient struct {
	hosts     gitea.Client
	templates git.Client
}

func (c gitClient) Provider(cfg ports.Config) (ports.Provider, error) { return c.hosts.Provider(cfg) }

func (c gitClient) FetchTemplate(ctx context.Context, url, ref, dir string,
	creds *ports.Credentials) (scaffold.Tree, error) {
	return c.templates.FetchTemplate(ctx, url, ref, dir, creds)
}

// goProxyURL is the Go proxy the --go-proxy flag names, "" for off.
func goProxyURL(value string) string {
	if value == "off" {
		return ""
	}
	return value
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	if v == "" {
		return errors.New("must not be empty")
	}
	*l = append(*l, v)
	return nil
}
