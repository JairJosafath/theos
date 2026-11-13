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
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// The receiver of alerts (docs/plans/done/alerts.md, decisions 2 and 3): it
// serves POST /alerts/{namespace}/{name} on the Git events listener, checks
// each delivery against its AlertSource's Secret, reads it with the
// source's parser and puts its alerts in the source's inbox, in memory. It
// answers before anything is filed: the AlertSource's reconciler drains the
// inbox. It writes nothing to the API server.

// alertsRoute is the route an AlertSource's sender delivers to
// (model.AlertsURL).
const alertsRoute = "POST " + model.AlertsPath + "{namespace}/{name}"

// maxAlertBody is the largest delivery read; a longer one gets 413.
const maxAlertBody = 1 << 20

// maxInboxAlerts is how many alerts one source's inbox holds; a delivery
// that would take it past gets 503, so its sender retries.
const maxInboxAlerts = 1000

// DefaultAlertDrainTimeout is how long the receiver drains its inboxes on
// SIGTERM unless --alert-drain-timeout says otherwise.
const DefaultAlertDrainTimeout = 5 * time.Second

// alertDrainRetry is how long the drain on SIGTERM waits after a pass that
// failed, a Git host that is down, before it tries again.
const alertDrainRetry = 100 * time.Millisecond

// The results theos_alert_deliveries_total counts deliveries by, and
// alertDropped the alerts that were taken and never filed.
const (
	alertAccepted     = "accepted"
	alertTooLarge     = "too_large"
	alertNotFound     = "not_found"
	alertUnauthorized = "unauthorized"
	alertBadRequest   = "bad_request"
	alertUnavailable  = "unavailable"
	alertDropped      = "dropped"
)

// alertDeliveries counts the deliveries by what became of them, and the
// alerts an inbox held when the operator stopped, by result "dropped". A
// delivery nobody authenticated is counted here alone: it writes nothing to
// the API server.
var alertDeliveries = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "theos_alert_deliveries_total",
	Help: "Deliveries to an AlertSource, by result; result=\"dropped\" counts the alerts taken and never filed " +
		"because the operator stopped first.",
}, []string{"result"})

func init() {
	ctrlmetrics.Registry.MustRegister(alertDeliveries)
}

// AlertDrainer files what an AlertSource's inbox holds; the AlertSource's
// reconciler is one.
type AlertDrainer interface {
	DrainAlerts(ctx context.Context, key types.NamespacedName) error
}

// queuedAlert is an alert in an inbox: what it says, when the receiver took
// it, and its place in the receiver's count of alerts.
type queuedAlert struct {
	model.Alert
	At  time.Time
	seq uint64
}

// alertCounts are what an inbox counts of its deliveries until a status
// write takes them: the deliveries taken and the last, and the alerts they
// held or said were left out past the ones read (model.ParseAlerts' more).
type alertCounts struct {
	Deliveries, Unread         int64
	LastDelivery, LastUnreadAt time.Time
}

func (c alertCounts) empty() bool { return c.Deliveries == 0 && c.Unread == 0 }

// alertInbox is one AlertSource's inbox.
type alertInbox struct {
	alerts []queuedAlert
	counts alertCounts
}

// AlertReceiver serves the deliveries of every AlertSource and keeps their
// alerts until its Drainer files them. It is mounted on the Git events
// listener (GitEventsReceiver.Alerts) and runs on the leader: Start does
// nothing until the manager stops, and then drains, on SIGTERM alone.
type AlertReceiver struct {
	// Client reads AlertSources from the cache.
	Client client.Reader
	// APIReader reads the source's Secret live: the operator caches no
	// Secrets.
	APIReader client.Reader
	// Drainer files what an inbox holds, when the operator stops.
	Drainer AlertDrainer
	// SignalContext is the signal handler's context, which only a signal
	// ends: when Start's context ends and it hasn't, leadership was lost
	// and nothing is drained.
	SignalContext context.Context
	// DrainTimeout is how long Start drains on SIGTERM
	// (--alert-drain-timeout); 0 means DefaultAlertDrainTimeout.
	DrainTimeout time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	inboxes map[types.NamespacedName]*alertInbox
	locks   map[types.NamespacedName]*sync.Mutex
	sinks   []chan event.TypedGenericEvent[types.NamespacedName]
	closed  bool
	seq     uint64
}

var _ manager.LeaderElectionRunnable = (*AlertReceiver)(nil)

// NeedLeaderElection is true: the inbox is drained by the AlertSource's
// reconciler, which runs on the leader.
func (a *AlertReceiver) NeedLeaderElection() bool { return true }

func (a *AlertReceiver) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Source enqueues an AlertSource when a delivery put something in its inbox.
// Call it before the manager starts.
func (a *AlertReceiver) Source() source.Source {
	ch := make(chan event.TypedGenericEvent[types.NamespacedName], gitEventsBuffer)
	a.mu.Lock()
	a.sinks = append(a.sinks, ch)
	a.mu.Unlock()
	return source.Channel(ch, handler.TypedEnqueueRequestsFromMapFunc(
		func(_ context.Context, key types.NamespacedName) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: key}}
		}))
}

// Handler serves POST /alerts/{namespace}/{name} alone.
func (a *AlertReceiver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(alertsRoute, a.serve)
	return mux
}

// answerAlert counts a delivery by result and answers it.
func answerAlert(w http.ResponseWriter, result string, status int, message string) {
	alertDeliveries.WithLabelValues(result).Inc()
	if status == http.StatusAccepted {
		w.WriteHeader(status)
		return
	}
	http.Error(w, message, status)
}

// serve reads one delivery: 503 once the operator stops or while the inbox
// is full, 413 past maxAlertBody, 404 when the AlertSource is missing, 401
// when the delivery doesn't carry what its Secret checks (or there is no
// Secret to check it), 400 for a body that isn't the type's shape, and 202
// once its alerts are in the inbox. Nothing of an alert's text is logged.
func (a *AlertReceiver) serve(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	key := types.NamespacedName{Namespace: req.PathValue("namespace"), Name: req.PathValue("name")}
	log := logf.FromContext(ctx).WithName("alerts").WithValues("alertsource", key.String())
	if a.isClosed() {
		answerAlert(w, alertUnavailable, http.StatusServiceUnavailable, "the operator is stopping; deliver again")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxAlertBody))
	if err != nil {
		if errors.As(err, new(*http.MaxBytesError)) {
			answerAlert(w, alertTooLarge, http.StatusRequestEntityTooLarge, "the delivery is larger than 1 MiB")
			return
		}
		answerAlert(w, alertBadRequest, http.StatusBadRequest, "could not read the delivery")
		return
	}
	var src c4v1beta1.AlertSource
	if err := a.Client.Get(ctx, key, &src); err != nil {
		if apierrors.IsNotFound(err) {
			answerAlert(w, alertNotFound, http.StatusNotFound, "no such AlertSource")
			return
		}
		log.Error(err, "Could not read the AlertSource of a delivery")
		answerAlert(w, alertUnavailable, http.StatusServiceUnavailable, "could not read the AlertSource")
		return
	}
	var secret corev1.Secret
	err = a.APIReader.Get(ctx, client.ObjectKey{Namespace: src.Namespace, Name: src.Spec.SecretRef.Name}, &secret)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "Could not read the AlertSource's Secret")
		answerAlert(w, alertUnavailable, http.StatusServiceUnavailable, "could not read the AlertSource's Secret")
		return
	}
	typ := string(src.Spec.Type)
	if !model.AlertDeliveryAuthentic(typ, secret.Data, req.Header.Get, body) {
		log.Info("Refused a delivery that doesn't carry what the AlertSource's Secret checks", "remote",
			req.RemoteAddr)
		answerAlert(w, alertUnauthorized, http.StatusUnauthorized, "the delivery's credential is missing or wrong")
		return
	}
	alerts, more, err := model.ParseAlerts(typ, req.Header.Get("Sentry-Hook-Resource"), body,
		model.AlertLabels{Container: src.Spec.ContainerLabel, Namespace: src.Spec.NamespaceLabel})
	if err != nil {
		log.Info("Refused a delivery that isn't the AlertSource's type's shape", "type", typ)
		// A fixed answer: the parser's words would echo the body back.
		answerAlert(w, alertBadRequest, http.StatusBadRequest, "the delivery is not a "+typ+" body")
		return
	}
	if !a.put(key, alerts, more) {
		log.Info("Refused a delivery: the AlertSource's inbox is full or the operator is stopping",
			"alerts", len(alerts))
		answerAlert(w, alertUnavailable, http.StatusServiceUnavailable, "the inbox is full; deliver again later")
		return
	}
	log.V(1).Info("Took a delivery", "alerts", len(alerts), "unread", more)
	a.notify(key)
	answerAlert(w, alertAccepted, http.StatusAccepted, "")
}

func (a *AlertReceiver) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

// put adds a delivery's alerts to key's inbox and counts it; false when the
// receiver is closed or the inbox has no room for all of them.
func (a *AlertReceiver) put(key types.NamespacedName, alerts []model.Alert, more int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return false
	}
	if a.inboxes == nil {
		a.inboxes = map[types.NamespacedName]*alertInbox{}
	}
	in := a.inboxes[key]
	if in == nil {
		in = &alertInbox{}
		a.inboxes[key] = in
	}
	if len(in.alerts)+len(alerts) > maxInboxAlerts {
		return false
	}
	now := a.now()
	for _, alert := range alerts {
		a.seq++
		in.alerts = append(in.alerts, queuedAlert{Alert: alert, At: now, seq: a.seq})
	}
	in.counts.Deliveries++
	in.counts.LastDelivery = now
	if more > 0 {
		in.counts.Unread += int64(more)
		in.counts.LastUnreadAt = now
	}
	return true
}

// notify enqueues key's AlertSource without blocking; a full channel drops
// it, and the reconciler's poll drains the inbox all the same.
func (a *AlertReceiver) notify(key types.NamespacedName) {
	a.mu.Lock()
	sinks := a.sinks
	a.mu.Unlock()
	for _, ch := range sinks {
		select {
		case ch <- event.TypedGenericEvent[types.NamespacedName]{Object: key}:
		default:
		}
	}
}

// lock gives its caller key's inbox to drain alone: the reconciler and the
// drain on SIGTERM never file the same alert side by side. The result
// unlocks it.
func (a *AlertReceiver) lock(key types.NamespacedName) func() {
	a.mu.Lock()
	if a.locks == nil {
		a.locks = map[types.NamespacedName]*sync.Mutex{}
	}
	l := a.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		a.locks[key] = l
	}
	a.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// peek is what key's inbox holds: its first alert, if any, and its counts.
func (a *AlertReceiver) peek(key types.NamespacedName) (first *queuedAlert, counts alertCounts) {
	a.mu.Lock()
	defer a.mu.Unlock()
	in := a.inboxes[key]
	if in == nil {
		return nil, alertCounts{}
	}
	if len(in.alerts) > 0 {
		q := in.alerts[0]
		first = &q
	}
	return first, in.counts
}

// taken drops from key's inbox the alert seq, when it is still there, and
// the counts a status write took.
func (a *AlertReceiver) taken(key types.NamespacedName, seq uint64, counts alertCounts) {
	a.mu.Lock()
	defer a.mu.Unlock()
	in := a.inboxes[key]
	if in == nil {
		return
	}
	in.alerts = slices.DeleteFunc(in.alerts, func(q queuedAlert) bool { return seq != 0 && q.seq == seq })
	in.counts.Deliveries = max(in.counts.Deliveries-counts.Deliveries, 0)
	in.counts.Unread = max(in.counts.Unread-counts.Unread, 0)
	if len(in.alerts) == 0 && in.counts.empty() {
		delete(a.inboxes, key)
	}
}

// forget empties key's inbox: its AlertSource is gone.
func (a *AlertReceiver) forget(key types.NamespacedName) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.inboxes, key)
}

// pending are the AlertSources whose inbox holds anything.
func (a *AlertReceiver) pending() []types.NamespacedName {
	a.mu.Lock()
	defer a.mu.Unlock()
	keys := make([]types.NamespacedName, 0, len(a.inboxes))
	for key := range a.inboxes {
		keys = append(keys, key)
	}
	return keys
}

// Start waits for the manager to stop, then stops taking deliveries (503)
// and, on SIGTERM, drains the inboxes until DrainTimeout. When its context
// ends and the signal handler's hasn't, leadership was lost: another
// operator may already lead, so it drains nothing. What is left is counted
// as dropped and logged by AlertSource and count.
func (a *AlertReceiver) Start(ctx context.Context) error {
	<-ctx.Done()
	log := logf.FromContext(ctx).WithName("alerts")
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	if a.SignalContext == nil || a.SignalContext.Err() == nil {
		log.Info("Stopped without a signal: leadership is lost, so the inboxes are not drained")
		a.drop(ctx)
		return nil
	}
	timeout := a.DrainTimeout
	if timeout <= 0 {
		timeout = DefaultAlertDrainTimeout
	}
	drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	for {
		keys := a.pending()
		if len(keys) == 0 {
			return nil
		}
		if drain.Err() != nil {
			a.drop(ctx)
			return nil
		}
		failed := false
		for _, key := range keys {
			if err := a.Drainer.DrainAlerts(drain, key); err != nil {
				failed = true
				log.Info("Could not drain an AlertSource's inbox; trying again until the deadline",
					"alertsource", key.String(), "error", err.Error())
			}
		}
		if !failed {
			continue
		}
		select {
		case <-drain.Done():
			a.drop(ctx)
			return nil
		case <-time.After(alertDrainRetry):
		}
	}
}

// drop empties every inbox, counting and logging the alerts it held.
func (a *AlertReceiver) drop(ctx context.Context) {
	a.mu.Lock()
	inboxes := a.inboxes
	a.inboxes = nil
	a.mu.Unlock()
	for key, in := range inboxes {
		if len(in.alerts) == 0 {
			continue
		}
		alertDeliveries.WithLabelValues(alertDropped).Add(float64(len(in.alerts)))
		logf.FromContext(ctx).WithName("alerts").Info("Dropped the alerts an AlertSource's inbox held",
			"alertsource", key.String(), "count", len(in.alerts))
	}
}
