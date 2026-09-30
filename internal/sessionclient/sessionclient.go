// Package sessionclient is the heart of the Session client that runs inside a
// devcontainer: it registers a Session on the Container the repository belongs
// to, sends a heartbeat every 30 seconds, and deletes the Session on the way out.
// The operator marks it Stale if the heartbeats stop without that.
package sessionclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// DefaultHeartbeat is how often the client reports in. The operator marks a
// Session Stale after two minutes without one.
const DefaultHeartbeat = 30 * time.Second

// Options say which Container a Session is for and who is working.
type Options struct {
	// Namespace is the project, which is also its namespace.
	Namespace string
	// Container is the Container being worked on.
	Container string
	Actor     c4v1alpha1.Actor
	// Summary is a one-line description of the work.
	Summary   string
	Heartbeat time.Duration
	Log       *slog.Logger
}

// FromIdentity fills Namespace and Container from a repository's identity file.
func (o *Options) FromIdentity(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	id, err := scaffold.ParseIdentity(data)
	if err != nil {
		return err
	}
	o.Namespace, o.Container = id.Project, id.Container
	return nil
}

// FindIdentity returns the nearest .c4factory.yaml in dir or above it.
func FindIdentity(dir string) (string, error) {
	start, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	dir = start
	for {
		candidate := filepath.Join(dir, scaffold.IdentityPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s in %s or above; is this a repository C4 Factory scaffolded?",
				scaffold.IdentityPath, start)
		}
		dir = parent
	}
}

// Run registers a Session and keeps it alive until ctx is done, then deletes it.
// If someone deletes the Session meanwhile, it registers a new one.
func Run(ctx context.Context, c client.Client, opts Options) error {
	if opts.Namespace == "" || opts.Container == "" {
		return errors.New("namespace and container are required")
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = DefaultHeartbeat
	}
	if opts.Actor == "" {
		opts.Actor = c4v1alpha1.ActorHuman
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	name, err := register(ctx, c, opts)
	if err != nil {
		return err
	}
	log.Info("Registered Session", "session", name, "container", opts.Container, "namespace", opts.Namespace,
		"actor", opts.Actor)

	ticker := time.NewTicker(opts.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// ctx is done, so clean up with a short context of our own.
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := c.Delete(cleanup, &c4v1alpha1.Session{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: opts.Namespace},
			})
			if client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("delete Session %s: %w", name, err)
			}
			log.Info("Deleted Session", "session", name)
			return nil
		case <-ticker.C:
			err := heartbeat(ctx, c, opts.Namespace, name)
			switch {
			case apierrors.IsNotFound(err):
				log.Info("Session was deleted; registering a new one", "session", name)
				if name, err = register(ctx, c, opts); err != nil {
					log.Error("Could not register Session", "error", err)
				}
			case err != nil && ctx.Err() == nil:
				// Keep trying: a missed heartbeat or two is what Stale is for.
				log.Error("Could not send heartbeat", "session", name, "error", err)
			}
		}
	}
}

// register creates the Session and sends its first heartbeat.
func register(ctx context.Context, c client.Client, opts Options) (string, error) {
	session := &c4v1alpha1.Session{
		ObjectMeta: metav1.ObjectMeta{GenerateName: opts.Container + "-", Namespace: opts.Namespace},
		Spec: c4v1alpha1.SessionSpec{
			ContainerRef: c4v1alpha1.LocalRef{Name: opts.Container},
			Actor:        opts.Actor,
			Summary:      opts.Summary,
		},
	}
	if err := c.Create(ctx, session); err != nil {
		return "", fmt.Errorf("register Session on Container %s/%s: %w", opts.Namespace, opts.Container, err)
	}
	if err := heartbeat(ctx, c, opts.Namespace, session.Name); err != nil {
		return "", fmt.Errorf("first heartbeat of Session %s: %w", session.Name, err)
	}
	return session.Name, nil
}

// heartbeat marks the Session Active now. It is a merge patch of the status
// alone, so it needs no read and never conflicts.
func heartbeat(ctx context.Context, c client.Client, namespace, name string) error {
	patch, err := json.Marshal(map[string]any{"status": map[string]any{
		"phase":         c4v1alpha1.SessionActive,
		"lastHeartbeat": metav1.Now().UTC().Format(time.RFC3339),
	}})
	if err != nil {
		return err
	}
	session := &c4v1alpha1.Session{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	return c.Status().Patch(ctx, session, client.RawPatch(types.MergePatchType, patch))
}
