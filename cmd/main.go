/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
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
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/features"
	githubdeploy "github.com/JairJosafath/c4factory/internal/adapters/github"
	"github.com/JairJosafath/c4factory/internal/buildinfo"
	"github.com/JairJosafath/c4factory/internal/controller"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(c4v1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var webhookPort int
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var sessionStaleAfter time.Duration
	var maxAgentSandboxes int
	var leaseDuration time.Duration
	var workspaceMemory string
	var sandboxGoCache string
	var goProxy string
	var sessionFeature, sessionBinaryURL string
	var sharedSecrets, sharedPullSecrets stringList
	var uiServiceAccount string
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
	flag.StringVar(&sandboxGoCache, "sandbox-go-cache", "",
		"When set, a directory on the node, such as /var/cache/c4factory/go/sandbox, that agent sandboxes "+
			"share as their Go and golangci-lint build cache, so a round doesn't compile what an earlier one did. "+
			"Never the CI runner's cache: agents run untrusted code, and Go trusts its cache.")
	flag.StringVar(&goProxy, "go-proxy", controller.DefaultGoProxy,
		"The Go proxy asked whether go get finds a released Go module whose path is outside c4factory.local/; "+
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
		"URL of a linux c4session binary for the feature to download; ${arch} becomes amd64 or arm64.")
	flag.Var(&sharedSecrets, "shared-secret",
		"A Secret in the operator's namespace (POD_NAMESPACE) to copy into every project namespace labeled "+
			"c4.factory.io/shared-secrets=true, such as claude-credentials; repeatable. None: nothing is shared.")
	flag.Var(&sharedPullSecrets, "shared-pull-secret",
		"An image pull Secret in the operator's namespace to share like --shared-secret; repeatable.")
	flag.StringVar(&uiServiceAccount, "ui-service-account", "",
		"The UI's ServiceAccount, as namespace/name, let read the pods and their metrics in every project "+
			"namespace for the Factory view's health. None: the UI sees no agent sandboxes.")
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" && len(sharedSecrets)+len(sharedPullSecrets) > 0 {
		setupLog.Error(nil, "--shared-secret and --shared-pull-secret need POD_NAMESPACE, the operator's namespace")
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
		OperatorVersion:   buildinfo.Version,
		ChartVersion:      os.Getenv("C4_CHART_VERSION"),
		UIServiceAccount:  uiAccount,
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
	// Each DeployTarget member's adapter, by the member's name.
	deployers := ports.Deployers{model.DeployMemberGitHub: githubdeploy.New(gitprovider.Client{})}
	if err := (&controller.ContainerReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		APIReader:       mgr.GetAPIReader(),
		Git:             gitprovider.Client{},
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
		Git:               gitprovider.Client{},
		MaxSandboxes:      maxAgentSandboxes,
		WorkspaceMemory:   workspaceQuantity,
		SandboxGoCache:    sandboxGoCache,
		SharedPullSecrets: sharedPullSecrets,
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
		Git:       gitprovider.Client{},
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
		Git:             gitprovider.Client{},
		Recorder:        mgr.GetEventRecorder("brief"),
		SharedSecrets:   sharedSecrets,
		SourceNamespace: podNamespace,
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
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager", "version", buildinfo.Version, "chartVersion", os.Getenv("C4_CHART_VERSION"))
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
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
