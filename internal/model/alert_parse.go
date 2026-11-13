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

package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The parsers of an AlertSource's deliveries (docs/plans/alerts.md, decisions
// 5 to 7): each reads its sender's body into alerts and makes no call. A body
// that isn't the sender's shape is an error, which the receiver answers 400;
// a delivery that is the sender's but concerns no alert gives none.

// Sentry's resources, as its Sentry-Hook-Resource header names them.
const (
	SentryResourceIssueAlert = "event_alert"
	SentryResourceIssue      = "issue"
)

// The words a delivery says an alert's state with.
const (
	alertStatusFiring   = "firing"
	alertStatusResolved = "resolved"
)

// ParseAlerts reads the delivery body of an AlertSource of type typ.
// resource is Sentry's Sentry-Hook-Resource header, unused by the others;
// labels are the AlertSource's. more counts the alerts the delivery held, or
// says its sender left out, past the ones read.
func ParseAlerts(typ, resource string, body []byte, labels AlertLabels) (alerts []Alert, more int, err error) {
	switch typ {
	case AlertTypeAlertmanager:
		return ParseAlertmanager(body, labels)
	case AlertTypeSentry:
		alerts, err = ParseSentry(resource, body, labels)
		return alerts, 0, err
	case AlertTypeGeneric:
		alerts, err = ParseGenericAlert(body, labels)
		return alerts, 0, err
	}
	return nil, 0, fmt.Errorf("unknown alert source type %q", typ)
}

// jsonText is a JSON string, number or boolean read as text; anything else
// reads as empty, so a field of the wrong type is a missing one, never an
// error.
type jsonText string

// UnmarshalJSON implements json.Unmarshaler.
func (t *jsonText) UnmarshalJSON(data []byte) error {
	*t = ""
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	switch data[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*t = jsonText(s)
	case '{', '[', 'n':
	default:
		*t = jsonText(data)
	}
	return nil
}

// alertTime reads an RFC 3339 time; anything else, and the zero time senders
// write for "not yet", is the zero time.
func alertTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
	if err != nil || t.Year() < 1970 {
		return time.Time{}
	}
	return t.UTC()
}

// maxAlertsUnread is the most alerts one delivery is taken to have left
// unread: truncatedAlerts is its sender's word, and no sender has a million
// more to say.
const maxAlertsUnread = 1_000_000

// alertmanagerDelivery is Alertmanager's webhook body, version 4.
type alertmanagerDelivery struct {
	TruncatedAlerts int64  `json:"truncatedAlerts"`
	Status          string `json:"status"`
	ExternalURL     string `json:"externalURL"`
	Alerts          []struct {
		Status       string            `json:"status"`
		Labels       map[string]string `json:"labels"`
		Annotations  map[string]string `json:"annotations"`
		StartsAt     string            `json:"startsAt"`
		EndsAt       string            `json:"endsAt"`
		GeneratorURL string            `json:"generatorURL"`
	} `json:"alerts"`
}

// ParseAlertmanager reads Alertmanager's webhook body: one alert for each of
// its alerts, the first MaxAlertsPerDelivery; more counts the rest and the
// ones Alertmanager says it left out, never below zero nor past
// maxAlertsUnread. Its body has one shape: a field of another JSON type than
// that shape gives it (a label that is no string) is no Alertmanager's body,
// and an error. An alert's key is a digest of
// its labels, as Alertmanager's own fingerprint is, so a firing alert, its
// repeats and its resolution are the same alert and a change of annotations
// makes no new one.
func ParseAlertmanager(body []byte, labels AlertLabels) (alerts []Alert, more int, err error) {
	var d alertmanagerDelivery
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, 0, fmt.Errorf("not Alertmanager's webhook body: %w", err)
	}
	if len(d.Alerts) == 0 {
		return nil, 0, errors.New("not Alertmanager's webhook body: it holds no alerts")
	}
	more = int(min(max(d.TruncatedAlerts, 0), maxAlertsUnread))
	if len(d.Alerts) > MaxAlertsPerDelivery {
		more = min(more+len(d.Alerts)-MaxAlertsPerDelivery, maxAlertsUnread)
		d.Alerts = d.Alerts[:MaxAlertsPerDelivery]
	}
	labels = labels.withDefaults()
	alerts = make([]Alert, 0, len(d.Alerts))
	for _, in := range d.Alerts {
		status := in.Status
		if status == "" {
			status = d.Status
		}
		a := Alert{
			Key:         alertKey(append([]string{AlertTypeAlertmanager}, sortedPairs(in.Labels)...)...),
			Resolved:    status == alertStatusResolved,
			Name:        alertLine(in.Labels["alertname"], maxAlertName),
			Summary:     alertLine(firstNonEmpty(in.Annotations["summary"], in.Annotations["title"]), maxAlertSummary),
			Description: alertBlock(firstNonEmpty(in.Annotations["description"], in.Annotations["message"]), maxAlertDescription),
			StartsAt:    alertTime(in.StartsAt),
			URL:         firstAlertURL(in.GeneratorURL, d.ExternalURL),
			Container:   alertLine(in.Labels[labels.Container], maxAlertHint),
			Namespace:   alertLine(in.Labels[labels.Namespace], maxAlertHint),
		}
		if a.Resolved {
			a.EndsAt = alertTime(in.EndsAt)
		}
		finishAlert(&a, in.Labels, in.Annotations)
		alerts = append(alerts, a)
	}
	return alerts, more, nil
}

// sortedPairs are pairs as name=value, in the names' order.
func sortedPairs(pairs map[string]string) []string {
	out := make([]string, 0, len(pairs))
	for name, value := range pairs {
		out = append(out, strconv.Quote(name)+"="+value)
	}
	slices.Sort(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// finishAlert gives a its labels and annotations, cut, and a name when its
// sender gave none.
func finishAlert(a *Alert, labels, annotations map[string]string) {
	var droppedLabels, droppedAnnotations int
	a.Labels, droppedLabels = alertPairs(labels)
	a.Annotations, droppedAnnotations = alertPairs(annotations)
	a.Truncated = droppedLabels + droppedAnnotations
	if a.Name == "" {
		a.Name = unnamedAlert
	}
}

// sentryDelivery is the body of Sentry's integration webhooks, as far as the
// issue alert (resource event_alert) and the issue (resource issue) go.
type sentryDelivery struct {
	Action jsonText `json:"action"`
	Data   struct {
		Event         *sentryEvent `json:"event"`
		Issue         *sentryIssue `json:"issue"`
		TriggeredRule jsonText     `json:"triggered_rule"`
	} `json:"data"`
}

type sentryEvent struct {
	URL         jsonText        `json:"url"`
	WebURL      jsonText        `json:"web_url"`
	IssueURL    jsonText        `json:"issue_url"`
	IssueID     jsonText        `json:"issue_id"`
	EventID     jsonText        `json:"event_id"`
	Title       jsonText        `json:"title"`
	Culprit     jsonText        `json:"culprit"`
	Message     jsonText        `json:"message"`
	Level       jsonText        `json:"level"`
	Platform    jsonText        `json:"platform"`
	Release     jsonText        `json:"release"`
	Environment jsonText        `json:"environment"`
	Datetime    jsonText        `json:"datetime"`
	Timestamp   jsonText        `json:"timestamp"`
	Project     jsonText        `json:"project"`
	Tags        json.RawMessage `json:"tags"`
}

type sentryIssue struct {
	ID        jsonText        `json:"id"`
	Title     jsonText        `json:"title"`
	Culprit   jsonText        `json:"culprit"`
	WebURL    jsonText        `json:"web_url"`
	Permalink jsonText        `json:"permalink"`
	Project   json.RawMessage `json:"project"`
}

// ParseSentry reads a delivery of a Sentry integration by its resource. An
// issue alert (event_alert) is one firing alert, whose key is a digest of
// the Sentry issue the event belongs to, so every event of an issue is the
// same alert. An issue that was resolved (issue, action resolved) is that
// alert, resolved. Every other resource and action is Sentry's but no alert:
// no alerts and no error. A Sentry alert names no namespace: its project
// leads to its Container (AlertContainer).
func ParseSentry(resource string, body []byte, labels AlertLabels) ([]Alert, error) {
	var d sentryDelivery
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("not a Sentry webhook body: %w", err)
	}
	switch resource {
	case SentryResourceIssueAlert:
		if d.Data.Event == nil {
			return nil, errors.New("not a Sentry issue alert: it holds no data.event")
		}
		return []Alert{sentryIssueAlert(d, labels.withDefaults().Container)}, nil
	case SentryResourceIssue:
		if d.Data.Issue == nil {
			return nil, errors.New("not a Sentry issue delivery: it holds no data.issue")
		}
		if d.Action != alertStatusResolved {
			return nil, nil
		}
		return []Alert{sentryIssueResolved(*d.Data.Issue)}, nil
	}
	return nil, nil
}

func sentryIssueAlert(d sentryDelivery, containerLabel string) Alert {
	e := d.Data.Event
	tags := sentryTags(e.Tags)
	project := sentryProjectOfURL(string(e.URL))
	if project == "" {
		project = string(e.Project)
	}
	annotations := map[string]string{}
	for name, value := range map[string]jsonText{"rule": d.Data.TriggeredRule, "level": e.Level,
		"platform": e.Platform, "release": e.Release, "environment": e.Environment, "event": e.EventID} {
		if strings.TrimSpace(string(value)) != "" {
			annotations[name] = string(value)
		}
	}
	a := Alert{
		Key:       sentryKey(string(e.IssueID), project, string(e.Title)),
		Name:      alertLine(string(e.Title), maxAlertName),
		Summary:   alertLine(string(e.Culprit), maxAlertSummary),
		StartsAt:  alertTime(string(e.Datetime)),
		URL:       firstAlertURL(string(e.WebURL), string(e.IssueURL), string(e.URL)),
		Container: alertLine(tags[containerLabel], maxAlertHint),
		Project:   alertLine(project, maxAlertHint),
	}
	if message := alertBlock(string(e.Message), maxAlertDescription); message != a.Name {
		a.Description = message
	}
	if a.StartsAt.IsZero() {
		if seconds, err := strconv.ParseFloat(string(e.Timestamp), 64); err == nil && seconds > 0 && seconds < 1e11 {
			a.StartsAt = time.Unix(int64(seconds), 0).UTC()
		}
	}
	finishAlert(&a, tags, annotations)
	return a
}

func sentryIssueResolved(issue sentryIssue) Alert {
	var project struct {
		Slug jsonText `json:"slug"`
		ID   jsonText `json:"id"`
	}
	// A project of another shape names none.
	_ = json.Unmarshal(issue.Project, &project)
	slug := firstNonEmpty(string(project.Slug), string(project.ID))
	a := Alert{
		Key:      sentryKey(string(issue.ID), slug, string(issue.Title)),
		Resolved: true,
		Name:     alertLine(string(issue.Title), maxAlertName),
		Summary:  alertLine(string(issue.Culprit), maxAlertSummary),
		URL:      firstAlertURL(string(issue.WebURL), string(issue.Permalink)),
		Project:  alertLine(slug, maxAlertHint),
	}
	finishAlert(&a, nil, nil)
	return a
}

// sentryKey is the key of the Sentry issue id; an event that names no issue
// is told apart by its project and title.
func sentryKey(issue, project, title string) string {
	if issue = strings.TrimSpace(issue); issue != "" {
		return alertKey(AlertTypeSentry, "issue", issue)
	}
	return alertKey(AlertTypeSentry, "title", project, title)
}

// sentryTags reads an event's tags, which Sentry writes as [name, value]
// pairs and some of its bodies as {"key", "value"} objects. A tag of another
// shape is passed over.
func sentryTags(raw json.RawMessage) map[string]string {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	tags := map[string]string{}
	for _, entry := range entries {
		var pair []jsonText
		var object struct {
			Key   jsonText `json:"key"`
			Value jsonText `json:"value"`
		}
		switch {
		case json.Unmarshal(entry, &pair) == nil && len(pair) == 2:
			tags[string(pair[0])] = string(pair[1])
		case json.Unmarshal(entry, &object) == nil && object.Key != "":
			tags[string(object.Key)] = string(object.Value)
		}
	}
	return tags
}

// sentryProjectOfURL is the project slug in an event's API address,
// .../projects/<organization>/<project>/events/<id>/, or "".
func sentryProjectOfURL(address string) string {
	_, rest, found := strings.Cut(address, "/projects/")
	if !found {
		return ""
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || parts[2] != "events" {
		return ""
	}
	return parts[1]
}

// genericAlert is the body a generic AlertSource takes: one alert, in the
// factory's own words, for a tool that can post JSON it is told the shape of.
type genericAlert struct {
	Key         jsonText            `json:"key"`
	Status      jsonText            `json:"status"`
	Title       jsonText            `json:"title"`
	Summary     jsonText            `json:"summary"`
	Description jsonText            `json:"description"`
	Container   jsonText            `json:"container"`
	Namespace   jsonText            `json:"namespace"`
	StartsAt    jsonText            `json:"startsAt"`
	EndsAt      jsonText            `json:"endsAt"`
	URL         jsonText            `json:"url"`
	Labels      map[string]jsonText `json:"labels"`
	Annotations map[string]jsonText `json:"annotations"`
}

// ParseGenericAlert reads the generic body: one JSON object with a title,
// and optionally key, status (firing or resolved), summary, description,
// container, namespace, startsAt, endsAt, url, labels and annotations. Its
// key is a digest of key, or of the title when it gives none. A label of the
// AlertSource's names (names) says the Container or the namespace when the
// field of that name doesn't.
func ParseGenericAlert(body []byte, names AlertLabels) ([]Alert, error) {
	var in genericAlert
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("not a generic alert: %w", err)
	}
	name := alertLine(string(in.Title), maxAlertName)
	if name == "" {
		return nil, errors.New("not a generic alert: it has no title")
	}
	switch in.Status {
	case "", alertStatusFiring, alertStatusResolved:
	default:
		return nil, fmt.Errorf("not a generic alert: status %q is neither firing nor resolved",
			alertLine(string(in.Status), maxAlertHint))
	}
	names = names.withDefaults()
	labels, annotations := textPairs(in.Labels), textPairs(in.Annotations)
	a := Alert{
		Key:         alertKey(AlertTypeGeneric, firstNonEmpty(string(in.Key), string(in.Title))),
		Resolved:    in.Status == alertStatusResolved,
		Name:        name,
		Summary:     alertLine(string(in.Summary), maxAlertSummary),
		Description: alertBlock(string(in.Description), maxAlertDescription),
		StartsAt:    alertTime(string(in.StartsAt)),
		URL:         alertURL(string(in.URL)),
		Container:   alertLine(firstNonEmpty(string(in.Container), labels[names.Container]), maxAlertHint),
		Namespace:   alertLine(firstNonEmpty(string(in.Namespace), labels[names.Namespace]), maxAlertHint),
	}
	if a.Resolved {
		a.EndsAt = alertTime(string(in.EndsAt))
	}
	finishAlert(&a, labels, annotations)
	return []Alert{a}, nil
}

func textPairs(in map[string]jsonText) map[string]string {
	out := make(map[string]string, len(in))
	for name, value := range in {
		out[name] = string(value)
	}
	return out
}
