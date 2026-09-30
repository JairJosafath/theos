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

// Command c4session registers the work in a devcontainer as a Session on its
// Container, so humans and agents show up in the C4 Factory model.
//
//	c4session run    register a Session and keep it alive until stopped
//	c4session start  the same, in the background (for postStartCommand)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/sessionclient"
)

const usage = `c4session registers the work in a devcontainer as a Session on its Container.

Usage:
  c4session run   [flags]   register a Session and keep it alive until stopped
  c4session start [flags]   the same, in the background; never fails (for postStartCommand)

Flags:
`

type flags struct {
	identity  string
	actor     string
	summary   string
	heartbeat time.Duration
	logFile   string
}

func main() {
	var f flags
	flag.StringVar(&f.identity, "identity", "",
		"Path to the repository's .c4factory.yaml (default: the nearest one above the working directory)")
	flag.StringVar(&f.actor, "actor", envOr("C4_SESSION_ACTOR", string(c4v1alpha1.ActorHuman)),
		"Who is working: agent or human")
	flag.StringVar(&f.summary, "summary", os.Getenv("C4_SESSION_SUMMARY"), "One line about the work")
	flag.DurationVar(&f.heartbeat, "heartbeat", sessionclient.DefaultHeartbeat, "How often to report in")
	flag.StringVar(&f.logFile, "log-file", "/tmp/c4session.log", "Where start writes the background client's log")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage); flag.PrintDefaults() }
	if len(os.Args) < 2 {
		flag.Usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	// flag.CommandLine also carries --kubeconfig, registered by controller-runtime.
	if err := flag.CommandLine.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	switch cmd {
	case "run":
		if err := run(f, log); err != nil {
			log.Error("Session client stopped", "error", err)
			os.Exit(1)
		}
	case "start":
		start(f, log)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

// run registers the Session and blocks until SIGINT or SIGTERM, then deletes it.
func run(f flags, log *slog.Logger) error {
	opts, err := options(f, log)
	if err != nil {
		return err
	}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("no cluster to report to: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		return err
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return sessionclient.Run(ctx, c, opts)
}

func options(f flags, log *slog.Logger) (sessionclient.Options, error) {
	opts := sessionclient.Options{
		Actor:     c4v1alpha1.Actor(f.actor),
		Summary:   f.summary,
		Heartbeat: f.heartbeat,
		Log:       log,
	}
	if opts.Actor != c4v1alpha1.ActorAgent && opts.Actor != c4v1alpha1.ActorHuman {
		return opts, fmt.Errorf("actor must be agent or human, not %q", f.actor)
	}
	path := f.identity
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return opts, err
		}
		if path, err = sessionclient.FindIdentity(wd); err != nil {
			return opts, err
		}
	}
	return opts, opts.FromIdentity(path)
}

// start runs the client in the background, detached from this process, and
// always exits 0: a devcontainer must start even when no cluster is reachable.
func start(f flags, log *slog.Logger) {
	if _, err := options(f, log); err != nil {
		log.Warn("Not starting the Session client", "reason", err)
		return
	}
	logFile, err := os.OpenFile(f.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Warn("Not starting the Session client", "reason", err)
		return
	}
	defer func() { _ = logFile.Close() }()

	self, err := os.Executable()
	if err != nil {
		log.Warn("Not starting the Session client", "reason", err)
		return
	}
	args := append([]string{"run"}, os.Args[2:]...)
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Warn("Not starting the Session client", "reason", err)
		return
	}
	log.Info("Started the Session client in the background", "pid", cmd.Process.Pid, "log", f.logFile)
	_ = cmd.Process.Release()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
