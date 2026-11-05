/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/conversion"

	c4v1alpha1 "github.com/JairJosafath/theos/api/v1alpha1"
	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/features"
	"github.com/JairJosafath/theos/internal/adapters/argocd"
	"github.com/JairJosafath/theos/internal/adapters/cloudformation"
	"github.com/JairJosafath/theos/internal/adapters/git"
	"github.com/JairJosafath/theos/internal/adapters/gitea"
	"github.com/JairJosafath/theos/internal/adapters/github"
	"github.com/JairJosafath/theos/internal/adapters/gitlab"
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

	utilruntime.Must(c4v1alpha1.AddToScheme(scheme))
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
	if err := (&controller.ContainerReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		APIReader:       mgr.GetAPIReader(),
		Git:             gitClient{},
		ScaffoldOptions: scaffoldOptions,
		Recorder:        mgr.GetEventRecorder("container"),
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
		GoProxy:   goProxyURL(goProxy),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "crew")
		os.Exit(1)
	}
	if err := (&controller.BriefReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		APIReader:       mgr.GetAPIReader(),
		Git:             gitClient{},
		Issues:          github.Client{},
		Recorder:        mgr.GetEventRecorder("brief"),
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
	if err := (&controller.ChatReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		APIReader:  mgr.GetAPIReader(),
		ChatHosts:  matrix.Hosts(nil),
		Homeserver: chatHomeserver,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "chat")
		os.Exit(1)
	}
	if err := (&controller.ScorerReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
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
	// +kubebuilder:scaffold:builder

	webhookOn := webhookCert != nil || webhookCertPath != ""
	if webhookOn {
		// Every theos.factory.io kind converts between its versions here: each
		// v1alpha1 type is Convertible to its v1beta1 hub
		// (docs/plans/done/api-versions.md, decision 3).
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
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager", "version", buildinfo.Version, "chartVersion", os.Getenv("THEOS_CHART_VERSION"))
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
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

func (c gitClient) FetchTemplate(ctx context.Context, url, ref string,
	creds *ports.Credentials) (scaffold.Tree, error) {
	return c.templates.FetchTemplate(ctx, url, ref, creds)
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
