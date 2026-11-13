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
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The drain of an AlertSource's inbox (docs/plans/done/alerts.md, decisions 2, 4
// and 8 to 11): what each alert does is model.AlertStep's; this file reads
// the facts it needs, makes the calls to the Git host it decides, and keeps
// status, which holds the counts.

// issueStateOpen is the state of an open issue on every Git host.
const issueStateOpen = "open"

// alertHost is the Git host a Container's alerts are filed on, and its
// repository there.
type alertHost struct {
	issues ports.Issues
	repo   ports.Repository
}

// alertPass is one drain of an AlertSource: the source, its settings and
// clock, and the Containers of its namespace.
type alertPass struct {
	r          *AlertSourceReconciler
	src        *c4v1beta1.AlertSource
	settings   model.AlertSettings
	now        time.Time
	routes     []model.AlertRoute
	places     []model.AlertPlace
	containers map[string]*c4v1beta1.Container
	hosts      map[string]alertHost
}

// drain files what key's inbox holds, an alert at a time with a status write
// each, and then makes the comments owed. It returns how long until the next
// comment is due; zero when none is.
func (r *AlertSourceReconciler) drain(ctx context.Context, key types.NamespacedName) (time.Duration, error) {
	unlock := r.Receiver.lock(key)
	defer unlock()
	var src c4v1beta1.AlertSource
	if err := r.APIReader.Get(ctx, key, &src); err != nil {
		if apierrors.IsNotFound(err) {
			r.Receiver.forget(key)
			return 0, nil
		}
		return 0, fmt.Errorf("reading AlertSource %s: %w", key, err)
	}
	p, err := r.newAlertPass(ctx, &src)
	if err != nil {
		return 0, err
	}
	for {
		q, counts := r.Receiver.peek(key)
		if q == nil && counts.empty() {
			break
		}
		status := src.Status.DeepCopy()
		addAlertCounts(status, counts)
		var seq uint64
		if q != nil {
			seq = q.seq
			// The clock is read for each alert: one taken after midnight
			// counts on its own day, however long the pass ran.
			p.now = r.now()
			if err := p.take(ctx, status, q.Alert, q.At); err != nil {
				// What was said before the step failed is written even
				// so, that it is said once (decision 11); the alert
				// stays in the inbox.
				if werr := r.writeStatus(ctx, &src, status); werr != nil {
					return 0, errors.Join(err, werr)
				}
				r.Receiver.taken(key, 0, counts)
				return 0, err
			}
		}
		if err := r.writeStatus(ctx, &src, status); err != nil {
			return 0, err
		}
		r.Receiver.taken(key, seq, counts)
	}
	status := src.Status.DeepCopy()
	p.now = r.now()
	wait, owedErr := p.owed(ctx, status)
	p.forgetGone(status)
	return wait, errors.Join(owedErr, r.writeStatus(ctx, &src, status))
}

// newAlertPass reads what a drain of src needs: its settings and the
// Containers of its namespace, with the namespaces their argoCD
// DeployTargets put them in.
func (r *AlertSourceReconciler) newAlertPass(ctx context.Context, src *c4v1beta1.AlertSource) (*alertPass, error) {
	p := &alertPass{r: r, src: src, now: r.now(), containers: map[string]*c4v1beta1.Container{},
		hosts:    map[string]alertHost{},
		settings: model.AlertSettings{DailyLimit: int(src.Spec.DailyLimit)}}
	if src.Spec.CommentInterval != nil {
		p.settings.CommentInterval = src.Spec.CommentInterval.Duration
	}
	for _, route := range src.Spec.Routes {
		p.routes = append(p.routes, model.AlertRoute{Container: route.Container, Namespace: route.Namespace,
			Project: route.Project})
	}
	var containers c4v1beta1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(src.Namespace)); err != nil {
		return nil, fmt.Errorf("listing the Containers of namespace %s: %w", src.Namespace, err)
	}
	var targets c4v1beta1.DeployTargetList
	if err := r.List(ctx, &targets, client.InNamespace(src.Namespace)); err != nil {
		return nil, fmt.Errorf("listing the DeployTargets of namespace %s: %w", src.Namespace, err)
	}
	destinations := map[string]string{}
	for _, t := range targets.Items {
		if t.Spec.ArgoCD != nil && t.Spec.ArgoCD.Destination.Namespace != "" {
			destinations[t.Name] = t.Spec.ArgoCD.Destination.Namespace
		}
	}
	for i := range containers.Items {
		c := &containers.Items[i]
		if !c.DeletionTimestamp.IsZero() {
			continue
		}
		p.containers[c.Name] = c
		place := model.AlertPlace{Container: c.Name}
		for _, d := range c.Spec.Deploy {
			if ns, ok := destinations[d.Target]; ok && !slices.Contains(place.Namespaces, ns) {
				place.Namespaces = append(place.Namespaces, ns)
			}
		}
		p.places = append(p.places, place)
	}
	return p, nil
}

// host is the Git host container's alerts are filed on.
func (p *alertPass) host(ctx context.Context, container string) (alertHost, error) {
	if h, ok := p.hosts[container]; ok {
		return h, nil
	}
	c := p.containers[container]
	provider, err := providerFor(ctx, p.r.Client, p.r.APIReader, p.r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
	if err != nil {
		return alertHost{}, fmt.Errorf("reaching the Git host of Container %s: %w", container, err)
	}
	h := alertHost{issues: provider, repo: provider.Repository(c.Name)}
	p.hosts[container] = h
	return h, nil
}

// take does what a's delivery, taken at at, does, and puts it in status. An
// error leaves in status only what was said before it, the old day settled:
// the caller writes that, and the alert stays in the inbox for the next
// pass.
func (p *alertPass) take(ctx context.Context, status *c4v1beta1.AlertSourceStatus, a model.Alert,
	at time.Time) error {
	log := logf.FromContext(ctx)
	records := alertRecordsOf(status.Alerts)
	var rec *model.AlertRecord
	if i := slices.IndexFunc(records, func(r model.AlertRecord) bool { return r.Key == a.Key }); i >= 0 &&
		p.containers[records[i].Container] != nil {
		rec = &records[i]
	}
	container, why := "", ""
	if rec != nil {
		container = rec.Container
	} else {
		container, why = model.AlertContainer(a, p.routes, p.places)
	}
	if container == "" {
		u := status.Unmatched
		if u == nil {
			u = &c4v1beta1.AlertSourceUnmatched{}
		}
		status.Unmatched = &c4v1beta1.AlertSourceUnmatched{Count: u.Count + 1, LastTime: alertTime(p.now),
			Message: why}
		log.V(1).Info("An alert matched no Container", "key", a.Key, "why", why)
		return nil
	}
	host, err := p.host(ctx, container)
	if err != nil {
		return err
	}
	// The old day is settled before the first step of a new one, which
	// starts the day's count afresh (decision 10).
	if err := p.settleDay(ctx, status, container); err != nil {
		return err
	}
	day, _ := alertDayOf(status.Containers, container)
	facts := model.AlertFacts{Record: rec, Day: day, Full: model.AlertRecordsFull(records, p.now)}
	if rec != nil && rec.Issue != 0 && !rec.Folded {
		issue, err := host.issues.GetIssue(ctx, host.repo, rec.Issue)
		switch {
		case errors.Is(err, ports.ErrIssueNotFound):
		case err != nil:
			return fmt.Errorf("reading issue %d of Container %s: %w", rec.Issue, container, err)
		default:
			facts.IssueOpen, facts.ClosedAt = issue.State == issueStateOpen, issue.ClosedAt
		}
	}
	d := model.AlertStep(a, container, facts, p.settings, p.now)
	switch d.Action {
	case model.AlertFile:
		number, isNew, err := p.file(ctx, host, a, container, at)
		if err != nil {
			return fmt.Errorf("filing an alert's issue for Container %s: %w", container, err)
		}
		d = d.Filed(number)
		if isNew {
			log.Info("Filed an alert's issue", "container", container, "issue", number, "key", a.Key)
		} else {
			log.Info("Found an alert's open issue by its marker", "container", container, "issue", number,
				"key", a.Key)
		}
	case model.AlertResolve:
		if err := host.issues.CommentIssue(ctx, host.repo, d.Issue, model.AlertResolvedComment(a, d, p.now)); err != nil {
			return fmt.Errorf("saying on issue %d of Container %s that its alert resolved: %w", d.Issue, container,
				err)
		}
	case model.AlertIgnore:
		return nil
	}
	putAlertDay(status, container, d.Day)
	if d.Action != model.AlertOverflow {
		status.Alerts = alertsOf(model.PutAlertRecord(records, d.Record))
	}
	return nil
}

// file returns the open source/monitor issue of a, found by its marker, or
// files it; isNew says it was filed now. The marker covers a status write
// lost after filing, and an alert whose record made room while its issue
// stayed open (decision 8).
func (p *alertPass) file(ctx context.Context, host alertHost, a model.Alert, container string, at time.Time) (
	number int, isNew bool, err error) {
	open, err := host.issues.ListIssues(ctx, host.repo, model.LabelMonitor)
	if err != nil {
		return 0, false, err
	}
	for _, issue := range open {
		if model.HasAlertMarker(issue.Body, p.src.Name, a.Key) {
			return issue.Number, false, nil
		}
	}
	if err := host.issues.EnsureLabels(ctx, host.repo, monitorIssueLabels()); err != nil {
		return 0, false, err
	}
	c := p.containers[container]
	var deployed []model.AlertDeployed
	for _, d := range c.Status.Deploys {
		deployed = append(deployed, model.AlertDeployed{Target: d.Target, Version: d.Version, Health: d.Health})
	}
	title, body := model.AlertIssue(a, model.AlertIssueFacts{Source: p.src.Name, Type: string(p.src.Spec.Type),
		Container: container, Deployed: deployed, At: at})
	issue, err := host.issues.CreateIssue(ctx, host.repo, title, body, model.AlertIssueLabels(), 0)
	if err != nil {
		return 0, false, err
	}
	return issue.Number, true, nil
}

// settleDay makes the "and N more" container's day is owed now, if any.
func (p *alertPass) settleDay(ctx context.Context, status *c4v1beta1.AlertSourceStatus, container string) error {
	day, ok := alertDayOf(status.Containers, container)
	if !ok {
		return nil
	}
	report, _ := model.AlertDayOwed(day, p.settings, p.now)
	if report == nil {
		return nil
	}
	host, err := p.host(ctx, container)
	if err != nil {
		return err
	}
	if err := host.issues.CommentIssue(ctx, host.repo, report.Issue,
		model.AlertFoldComment(*report, p.src.Name, p.settings)); err != nil {
		return fmt.Errorf("saying the alerts folded on %s on issue %d of Container %s: %w", report.Date,
			report.Issue, container, err)
	}
	putAlertDay(status, container, report.Day)
	return nil
}

// owed makes the comments the alerts' open issues and the Containers' days
// are owed now (model.AlertRecordOwed, model.AlertDayOwed), and returns how
// long until the next one is due. A failed comment is tried on the next
// pass; the others are made.
func (p *alertPass) owed(ctx context.Context, status *c4v1beta1.AlertSourceStatus) (time.Duration, error) {
	var wait time.Duration
	soonest := func(w time.Duration) {
		if w > 0 && (wait == 0 || w < wait) {
			wait = w
		}
	}
	var errs []error
	records := alertRecordsOf(status.Alerts)
	for i, rec := range records {
		if p.containers[rec.Container] == nil {
			continue
		}
		// Taken as open first: the Git host is asked only once a comment
		// would be due.
		report, w := model.AlertRecordOwed(rec, true, p.settings, p.now)
		soonest(w)
		if report == nil {
			continue
		}
		host, err := p.host(ctx, rec.Container)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		issue, err := host.issues.GetIssue(ctx, host.repo, rec.Issue)
		if err != nil && !errors.Is(err, ports.ErrIssueNotFound) {
			errs = append(errs, fmt.Errorf("reading issue %d of Container %s: %w", rec.Issue, rec.Container, err))
			continue
		}
		if err != nil || issue.State != issueStateOpen {
			// A closed issue is owed nothing: what it wasn't told stays
			// unsaid, and the host is not asked again at every pass.
			records[i].Reported = rec.Count
			continue
		}
		if err := host.issues.CommentIssue(ctx, host.repo, rec.Issue, model.AlertReportComment(*report, p.now)); err != nil {
			errs = append(errs, fmt.Errorf("counting an alert's repeats on issue %d of Container %s: %w", rec.Issue,
				rec.Container, err))
			continue
		}
		records[i] = report.Record
	}
	if len(records) > 0 {
		status.Alerts = alertsOf(records)
	}
	for _, entry := range slices.Clone(status.Containers) {
		if p.containers[entry.Name] == nil {
			continue
		}
		if err := p.settleDay(ctx, status, entry.Name); err != nil {
			errs = append(errs, err)
			continue
		}
		day, _ := alertDayOf(status.Containers, entry.Name)
		_, w := model.AlertDayOwed(day, p.settings, p.now)
		soonest(w)
	}
	return wait, errors.Join(errs...)
}

// forgetGone drops the records and the days of Containers that are gone, and
// the days before today that are owed nothing.
func (p *alertPass) forgetGone(status *c4v1beta1.AlertSourceStatus) {
	today := p.now.UTC().Format(time.DateOnly)
	status.Alerts = slices.DeleteFunc(status.Alerts, func(a c4v1beta1.AlertSourceAlert) bool {
		return p.containers[a.Container] == nil
	})
	status.Containers = slices.DeleteFunc(status.Containers, func(c c4v1beta1.AlertSourceContainerDay) bool {
		if p.containers[c.Name] == nil {
			return true
		}
		report, _ := model.AlertDayOwed(alertDayFrom(c), p.settings, p.now)
		return c.Date < today && report == nil
	})
	if len(status.Alerts) == 0 {
		status.Alerts = nil
	}
	if len(status.Containers) == 0 {
		status.Containers = nil
	}
}

// addAlertCounts adds what an inbox counted of its deliveries to status.
func addAlertCounts(status *c4v1beta1.AlertSourceStatus, counts alertCounts) {
	if counts.Deliveries > 0 {
		status.Deliveries += counts.Deliveries
		status.LastDeliveryTime = alertTime(counts.LastDelivery)
	}
	if counts.Unread > 0 {
		u := status.Unread
		if u == nil {
			u = &c4v1beta1.AlertSourceUnread{}
		}
		status.Unread = &c4v1beta1.AlertSourceUnread{Count: u.Count + counts.Unread,
			LastTime: alertTime(counts.LastUnreadAt)}
	}
}

// alertTime is t in status: nil when zero. Status holds seconds.
func alertTime(t time.Time) *metav1.Time {
	if t.IsZero() {
		return nil
	}
	mt := metav1.NewTime(t.UTC().Truncate(time.Second))
	return &mt
}

func timeOf(t *metav1.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// alertRecordsOf are status's alerts as the model reads them.
func alertRecordsOf(alerts []c4v1beta1.AlertSourceAlert) []model.AlertRecord {
	out := make([]model.AlertRecord, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, model.AlertRecord{Key: a.Key, Container: a.Container, Name: a.Name, Issue: int(a.Issue),
			Folded: a.Folded, Resolved: a.Resolved, FiledAt: timeOf(a.FiledAt), LastSeen: timeOf(a.LastSeen),
			Count: int(a.Count), Reported: int(a.Reported), ReportedAt: timeOf(a.ReportedAt),
			SaidResolved: a.SaidResolved})
	}
	return out
}

// alertsOf are records as status holds them.
func alertsOf(records []model.AlertRecord) []c4v1beta1.AlertSourceAlert {
	out := make([]c4v1beta1.AlertSourceAlert, 0, len(records))
	for _, r := range records {
		out = append(out, c4v1beta1.AlertSourceAlert{Key: r.Key, Container: r.Container, Name: r.Name,
			Issue: int32(r.Issue), Folded: r.Folded, Resolved: r.Resolved, FiledAt: alertTime(r.FiledAt),
			LastSeen: alertTime(r.LastSeen), Count: int64(r.Count), Reported: int64(r.Reported),
			ReportedAt: alertTime(r.ReportedAt), SaidResolved: r.SaidResolved})
	}
	return out
}

func alertDayFrom(c c4v1beta1.AlertSourceContainerDay) model.AlertDay {
	return model.AlertDay{Date: c.Date, Filed: int(c.Filed), LastIssue: int(c.LastIssue), Folded: int(c.Folded),
		Unrecorded: int(c.Unrecorded), FoldReported: int(c.FoldReported), FoldReportedAt: timeOf(c.FoldReportedAt)}
}

// alertDayOf is container's day as status holds it; false when it holds none.
func alertDayOf(days []c4v1beta1.AlertSourceContainerDay, container string) (model.AlertDay, bool) {
	for _, c := range days {
		if c.Name == container {
			return alertDayFrom(c), true
		}
	}
	return model.AlertDay{}, false
}

// putAlertDay puts day as container's day in status.
func putAlertDay(status *c4v1beta1.AlertSourceStatus, container string, day model.AlertDay) {
	entry := c4v1beta1.AlertSourceContainerDay{Name: container, Date: day.Date, Filed: int32(day.Filed),
		LastIssue: int32(day.LastIssue), Folded: int64(day.Folded), Unrecorded: int64(day.Unrecorded),
		FoldReported: int64(day.FoldReported), FoldReportedAt: alertTime(day.FoldReportedAt)}
	if i := slices.IndexFunc(status.Containers, func(c c4v1beta1.AlertSourceContainerDay) bool {
		return c.Name == container
	}); i >= 0 {
		status.Containers[i] = entry
		return
	}
	status.Containers = append(status.Containers, entry)
}
