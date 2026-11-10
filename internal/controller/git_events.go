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

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The receiver of the Git host's deliveries (docs/plans/done/git-events.md,
// decisions 5 to 8): a plain HTTP listener on the leader that checks each
// delivery against its GitProvider's Secret and hands what it concerns to the
// controllers, which read the host as a poll would. It writes nothing.

// gitEventsPath is the route a GitProvider's hook delivers to; the hook's URL
// is model.GitEventsURL, which ends in it.
const gitEventsPath = "POST /git-events/{namespace}/{name}"

// maxGitEventBody is the largest delivery read; a longer one gets 413.
const maxGitEventBody = 5 << 20

// gitEventsBuffer is how many Events each controller's channel holds; past it
// an Event is dropped, and polling covers it.
const gitEventsBuffer = 256

// GitEvent is a verified delivery: the GitProvider it came through and what
// it concerns.
type GitEvent struct {
	// Namespace and Provider name the GitProvider.
	Namespace, Provider string
	ports.Event
}

// deliveryRecord is the place, in the receiver's count of deliveries, of a
// GitProvider's last verified and last refused delivery; 0 when none.
type deliveryRecord struct {
	verified, refused uint64
}

// receiving says whether the last of the two was verified: false when the
// last was refused, or when there was none.
func (d deliveryRecord) receiving() bool { return d.verified > d.refused }

// GitEventsReceiver serves the Git host's deliveries
// (POST /git-events/{namespace}/{name}) and sends each verified one to every
// controller that subscribed with Source. Start serves them on BindAddress;
// Handler serves the same without a listener.
type GitEventsReceiver struct {
	// Client reads GitProviders from the cache.
	Client client.Reader
	// APIReader reads the credentials and the delivery Secret live: the
	// operator caches no Secrets.
	APIReader client.Reader
	// Git builds the GitProvider's Provider; one without ports.Events gets
	// 404.
	Git GitClient
	// BindAddress is where Start listens (--git-events-bind-address).
	BindAddress string

	mu        sync.Mutex
	sinks     []chan event.TypedGenericEvent[GitEvent]
	providers []chan event.TypedGenericEvent[GitEvent]
	records   map[types.NamespacedName]deliveryRecord
	count     uint64 // the deliveries recorded
}

var _ manager.LeaderElectionRunnable = (*GitEventsReceiver)(nil)

// NeedLeaderElection is true: the receiver feeds the controllers, which run
// on the leader.
func (g *GitEventsReceiver) NeedLeaderElection() bool { return true }

// Source is a buffered channel of the verified Events, mapped to requests by
// fn; on a nil receiver, a channel nothing sends to. Call it before the
// manager starts.
func (g *GitEventsReceiver) Source(fn func(context.Context, GitEvent) []reconcile.Request) source.Source {
	ch := make(chan event.TypedGenericEvent[GitEvent], gitEventsBuffer)
	if g != nil {
		g.mu.Lock()
		g.sinks = append(g.sinks, ch)
		g.mu.Unlock()
	}
	return source.Channel(ch, handler.TypedEnqueueRequestsFromMapFunc(fn))
}

// ProviderSource enqueues a GitProvider when its deliveries should move its
// Events condition: the first verified one after a refused one or none, and
// the first refused one after a verified one or none. On a nil receiver it
// enqueues nothing.
func (g *GitEventsReceiver) ProviderSource() source.Source {
	ch := make(chan event.TypedGenericEvent[GitEvent], gitEventsBuffer)
	if g != nil {
		g.mu.Lock()
		g.providers = append(g.providers, ch)
		g.mu.Unlock()
	}
	return source.Channel(ch, handler.TypedEnqueueRequestsFromMapFunc(
		func(_ context.Context, e GitEvent) []reconcile.Request {
			return []reconcile.Request{requestFor(e.Namespace, e.Provider)}
		}))
}

// Start serves the deliveries on BindAddress until ctx ends.
func (g *GitEventsReceiver) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("git-events")
	listener, err := net.Listen("tcp", g.BindAddress)
	if err != nil {
		return fmt.Errorf("listening for Git events on %s: %w", g.BindAddress, err)
	}
	server := &http.Server{Handler: g.Handler(), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Info("Serving Git events", "address", listener.Addr().String())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

// Handler serves POST /git-events/{namespace}/{name}.
func (g *GitEventsReceiver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(gitEventsPath, g.serve)
	return mux
}

// serve reads one delivery: 413 past maxGitEventBody, 404 when the
// GitProvider is missing or its adapter has no events, 401 when the signature
// fails, 202 once the Event is handed to the controllers.
func (g *GitEventsReceiver) serve(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	key := types.NamespacedName{Namespace: req.PathValue("namespace"), Name: req.PathValue("name")}
	log := logf.FromContext(ctx).WithName("git-events").WithValues("gitprovider", key.String())
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxGitEventBody))
	if err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			http.Error(w, "the delivery is larger than 5 MiB", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "could not read the delivery", http.StatusBadRequest)
		return
	}

	var gp c4v1beta1.GitProvider
	if err := g.Client.Get(ctx, key, &gp); err != nil {
		if apierrors.IsNotFound(err) {
			http.Error(w, "no such GitProvider", http.StatusNotFound)
			return
		}
		log.Error(err, "Could not read the GitProvider of a delivery")
		http.Error(w, "could not read the GitProvider", http.StatusServiceUnavailable)
		return
	}
	events, err := providerEvents(ctx, g.APIReader, g.Git, &gp)
	if err != nil {
		log.Error(err, "Could not build the GitProvider's adapter for a delivery")
		http.Error(w, "could not reach the GitProvider's adapter", http.StatusServiceUnavailable)
		return
	}
	if events == nil {
		http.Error(w, "the GitProvider's adapter has no events", http.StatusNotFound)
		return
	}
	secret, err := g.deliverySecret(ctx, &gp)
	if err != nil {
		log.Error(err, "Could not read the delivery Secret")
		http.Error(w, "could not read the delivery Secret", http.StatusServiceUnavailable)
		return
	}

	e, err := events.ParseEvent(req.Header.Get, body, secret)
	if errors.Is(err, ports.ErrBadSignature) {
		log.Info("Refused a delivery whose signature is missing or wrong", "remote", req.RemoteAddr)
		g.record(key, false)
		http.Error(w, "the delivery's signature is missing or wrong", http.StatusUnauthorized)
		return
	}
	// Past here the delivery is authentic: deliveries arrive.
	g.record(key, true)
	if err != nil {
		log.Info("Could not read a signed delivery", "kind", e.Kind, "error", err.Error())
		http.Error(w, "could not read the delivery", http.StatusBadRequest)
		return
	}
	log.V(1).Info("Received a delivery", "kind", e.Kind, "repository", e.Repository, "number", e.Number,
		"branch", e.Branch)
	g.send(ctx, GitEvent{Namespace: gp.Namespace, Provider: gp.Name, Event: e})
	w.WriteHeader(http.StatusAccepted)
}

// deliverySecret is the value of gp's delivery Secret, read live; a missing
// one or one with no value is an error, since nothing can be verified
// against it.
func (g *GitEventsReceiver) deliverySecret(ctx context.Context, gp *c4v1beta1.GitProvider) ([]byte, error) {
	var secret corev1.Secret
	name := model.GitEventsSecretName(gp.Name)
	if err := g.APIReader.Get(ctx, client.ObjectKey{Namespace: gp.Namespace, Name: name}, &secret); err != nil {
		return nil, fmt.Errorf("reading Secret %s: %w", name, err)
	}
	value := secret.Data[model.GitEventsSecretKey]
	if len(value) == 0 {
		return nil, fmt.Errorf("secret %s has no key %q", name, model.GitEventsSecretKey)
	}
	return value, nil
}

// send hands e to every controller without blocking; a full channel drops it.
func (g *GitEventsReceiver) send(ctx context.Context, e GitEvent) {
	g.mu.Lock()
	sinks := g.sinks
	g.mu.Unlock()
	for _, ch := range sinks {
		select {
		case ch <- event.TypedGenericEvent[GitEvent]{Object: e}:
		default:
			logf.FromContext(ctx).V(1).Info("Dropped a Git event: the controller's queue is full; polling covers it",
				"repository", e.Repository, "number", e.Number, "branch", e.Branch)
		}
	}
}

// record notes a verified or a refused delivery to key, and enqueues the
// GitProvider when it changes which of the two came last.
func (g *GitEventsReceiver) record(key types.NamespacedName, verified bool) {
	g.mu.Lock()
	if g.records == nil {
		g.records = map[types.NamespacedName]deliveryRecord{}
	}
	rec, known := g.records[key]
	was := rec.receiving()
	g.count++
	if verified {
		rec.verified = g.count
	} else {
		rec.refused = g.count
	}
	g.records[key] = rec
	providers := g.providers
	g.mu.Unlock()
	if known && was == rec.receiving() {
		return
	}
	for _, ch := range providers {
		select {
		case ch <- event.TypedGenericEvent[GitEvent]{Object: GitEvent{Namespace: key.Namespace, Provider: key.Name}}:
		default:
		}
	}
}

// deliveries is what the receiver remembers of key's deliveries since it
// started or since forgetDeliveries; known is false when there was none.
func (g *GitEventsReceiver) deliveries(key types.NamespacedName) (rec deliveryRecord, known bool) {
	if g == nil {
		return deliveryRecord{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, known = g.records[key]
	return rec, known
}

// forgetDeliveries drops what the receiver remembers of key's deliveries: the
// hook changed, and only deliveries after it count.
func (g *GitEventsReceiver) forgetDeliveries(key types.NamespacedName) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.records, key)
}

// providerEvents is gp's adapter as ports.Events, nil when the adapter has
// none.
func providerEvents(ctx context.Context, apiReader client.Reader, git GitClient,
	gp *c4v1beta1.GitProvider) (ports.Events, error) {
	p, err := gitProviderHost(ctx, apiReader, git, gp)
	if err != nil {
		return nil, err
	}
	events, _ := p.(ports.Events)
	return events, nil
}

// gitProviderHost is gp's adapter, built with the provider's Secret read
// live and where its packages are; an error when the Secret is missing or
// the adapter refuses the provider as it is configured.
func gitProviderHost(ctx context.Context, apiReader client.Reader, git GitClient,
	gp *c4v1beta1.GitProvider) (ports.Provider, error) {
	creds, found, err := readCredentials(ctx, apiReader, gp)
	if err != nil {
		return nil, fmt.Errorf("could not read Secret %s: %w", gp.Spec.CredentialsSecretRef.Name, err)
	}
	if !found {
		return nil, fmt.Errorf("secret %s of GitProvider %s not found", gp.Spec.CredentialsSecretRef.Name, gp.Name)
	}
	return git.Provider(providerConfig(gp, creds, packagesOf(gp)))
}

// The mapping of an Event to the objects it concerns (decision 7): the
// Container whose repository it names, on this GitProvider, with its Crews
// and Tasks; or the Brief whose repository it names, with its Plan Tasks.

// gitEventContainer is the Container whose repository e names, when it is on
// e's GitProvider.
func gitEventContainer(ctx context.Context, c client.Reader, e GitEvent) (*c4v1beta1.Container, bool) {
	var container c4v1beta1.Container
	if err := c.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: e.Repository}, &container); err != nil {
		return nil, false
	}
	return &container, container.Spec.GitProviderRef.Name == e.Provider
}

// gitEventBrief is the Brief whose repository e names, when it is on e's
// GitProvider.
func gitEventBrief(ctx context.Context, c client.Reader, e GitEvent) (*c4v1beta1.Brief, bool) {
	name, ok := strings.CutPrefix(e.Repository, model.BriefRepository(""))
	if !ok || name == "" {
		return nil, false
	}
	var brief c4v1beta1.Brief
	if err := c.Get(ctx, client.ObjectKey{Namespace: e.Namespace, Name: name}, &brief); err != nil {
		return nil, false
	}
	return &brief, brief.Spec.GitProviderRef.Name == e.Provider
}

// gitEventContainerRequest enqueues the Container e concerns.
func gitEventContainerRequest(c client.Reader) func(context.Context, GitEvent) []reconcile.Request {
	return func(ctx context.Context, e GitEvent) []reconcile.Request {
		if container, ok := gitEventContainer(ctx, c, e); ok {
			return []reconcile.Request{requestFor(container.Namespace, container.Name)}
		}
		return nil
	}
}

// gitEventCrewRequests enqueues the Crews of the Container e concerns.
func gitEventCrewRequests(c client.Reader) func(context.Context, GitEvent) []reconcile.Request {
	return func(ctx context.Context, e GitEvent) []reconcile.Request {
		container, ok := gitEventContainer(ctx, c, e)
		if !ok {
			return nil
		}
		var crews c4v1beta1.CrewList
		if err := c.List(ctx, &crews, client.InNamespace(e.Namespace),
			client.MatchingFields{indexContainer: container.Name}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(crews.Items))
		for _, crew := range crews.Items {
			reqs = append(reqs, requestFor(crew.Namespace, crew.Name))
		}
		return reqs
	}
}

// gitEventBriefRequest enqueues the Brief e concerns.
func gitEventBriefRequest(c client.Reader) func(context.Context, GitEvent) []reconcile.Request {
	return func(ctx context.Context, e GitEvent) []reconcile.Request {
		if brief, ok := gitEventBrief(ctx, c, e); ok {
			return []reconcile.Request{requestFor(brief.Namespace, brief.Name)}
		}
		return nil
	}
}

// gitEventTaskRequests enqueues the Tasks of the Container or Brief e
// concerns whose issue, Standard-change issue or pull request is e.Number, or
// whose branch is e.Branch; every unfinished one when e names neither.
func gitEventTaskRequests(c client.Reader) func(context.Context, GitEvent) []reconcile.Request {
	return func(ctx context.Context, e GitEvent) []reconcile.Request {
		_, isContainer := gitEventContainer(ctx, c, e)
		_, isBrief := gitEventBrief(ctx, c, e)
		if !isContainer && !isBrief {
			return nil
		}
		var keys []string
		if e.Number > 0 {
			keys = append(keys, taskRepositoryNumber(e.Repository, e.Number))
		}
		if e.Branch != "" {
			keys = append(keys, taskRepositoryBranch(e.Repository, e.Branch))
		}
		every := len(keys) == 0
		if every {
			keys = []string{e.Repository}
		}
		seen := map[string]bool{}
		var reqs []reconcile.Request
		for _, k := range keys {
			var tasks c4v1beta1.TaskList
			if err := c.List(ctx, &tasks, client.InNamespace(e.Namespace),
				client.MatchingFields{indexTaskRepository: k}); err != nil {
				return nil
			}
			for _, t := range tasks.Items {
				if seen[t.Name] || (every && isFinal(t.Status.Phase)) {
					continue
				}
				seen[t.Name] = true
				reqs = append(reqs, requestFor(t.Namespace, t.Name))
			}
		}
		return reqs
	}
}

// taskRepository is the repository a Task works in: its Container's, or its
// Brief's for a Plan Task; "" when it names neither.
func taskRepository(t *c4v1beta1.Task) string {
	if t.Spec.ContainerRef.Name != "" {
		return t.Spec.ContainerRef.Name
	}
	if t.Spec.BriefRef != nil && t.Spec.BriefRef.Name != "" {
		return model.BriefRepository(t.Spec.BriefRef.Name)
	}
	return ""
}

// taskRepositoryNumber is how an issue or a pull request appears in
// indexTaskRepository.
func taskRepositoryNumber(repo string, number int) string { return fmt.Sprintf("%s#%d", repo, number) }

// taskRepositoryBranch is how a branch appears in indexTaskRepository.
func taskRepositoryBranch(repo, branch string) string { return repo + "@" + branch }

// taskRepositoryKeys are a Task's keys in indexTaskRepository: its repository,
// its issue, Standard-change issue and pull request as <repository>#<number>,
// and its branches as <repository>@<branch>.
func taskRepositoryKeys(t *c4v1beta1.Task) []string {
	repo := taskRepository(t)
	if repo == "" {
		return nil
	}
	keys := []string{repo}
	numbers := []int32{t.Spec.Issue, t.Status.PullRequest}
	branches := []string{t.Status.Branch}
	if sc := t.Status.StandardChange; sc != nil {
		numbers = append(numbers, sc.Issue)
		branches = append(branches, sc.Branch)
	}
	for _, n := range numbers {
		if n > 0 {
			keys = append(keys, taskRepositoryNumber(repo, int(n)))
		}
	}
	for _, b := range branches {
		if b != "" {
			keys = append(keys, taskRepositoryBranch(repo, b))
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}
