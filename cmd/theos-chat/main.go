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

// Command theos-chat posts what the factory does in each Ready Chat's rooms,
// on Matrix or Slack, and runs the commands its people say there, as them
// (docs/plans/done/chat-matrix.md). It runs beside the operator with its own
// ServiceAccount (config/chat/rbac.yaml): it reads theos's objects, writes
// only its ConfigMaps, and impersonates the people the Chats name; the
// admission policies (config/policy) make sure each Chat's people named
// themselves.
package main

import (
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/matrix"
	"github.com/JairJosafath/theos/internal/adapters/slack"
	"github.com/JairJosafath/theos/internal/buildinfo"
	"github.com/JairJosafath/theos/internal/chatbot"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

func main() {
	var (
		metricsAddr, probeAddr string
		enableLeaderElection   bool
		interval               time.Duration
		uiURL                  string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to; 0 serves none.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Elect a leader, for more than one replica; it needs leases in its namespace.")
	flag.DurationVar(&interval, "interval", 5*time.Second,
		"How often each Chat's rooms are read and its news posted, besides when its namespace changes.")
	flag.StringVar(&uiURL, "ui-url", "",
		"The UI's address, http(s)://host[:port][/path], so each post links to its object's page; empty links none.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("setup")
	ui, err := model.ChatUIURL(uiURL)
	if err != nil {
		log.Error(err, "Refusing --ui-url", "ui-url", uiURL)
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(c4v1beta1.AddToScheme(scheme))

	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "theos-chat.factory.io",
	})
	if err != nil {
		log.Error(err, "Failed to make the manager")
		os.Exit(1)
	}
	// One adapter per chat host, by the name of its model.ChatSystem row. Each
	// reaches its host at the address the operator wrote into a Chat's
	// status.homeserver, so theos-chat takes no address of its own.
	hosts := map[string]ports.ChatHosts{model.ChatMatrix: matrix.Hosts(nil), model.ChatSlack: slack.Hosts(nil)}
	bot := &chatbot.Bot{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Config: mgr.GetConfig(),
		ChatHosts: hosts, Interval: interval, UIURL: ui}
	if err := bot.SetupWithManager(mgr); err != nil {
		log.Error(err, "Failed to set up theos-chat")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "Failed to set up the health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "Failed to set up the ready check")
		os.Exit(1)
	}
	log.Info("Starting theos-chat", "version", buildinfo.Version)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "Failed to run theos-chat")
		os.Exit(1)
	}
}
