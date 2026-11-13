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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The alerts of the tools that watch a running app (docs/plans/done/alerts.md):
// an AlertSource takes their deliveries, and a firing alert becomes a bug in
// its Container's repository. This file holds the alert every type's
// delivery is read into, and how an alert finds its Container.

// The types of an AlertSource: whose deliveries it takes.
const (
	AlertTypeAlertmanager = "alertmanager"
	AlertTypeSentry       = "sentry"
	AlertTypeGeneric      = "generic"
)

// AlertTypes are the types an AlertSource may have.
var AlertTypes = []string{AlertTypeAlertmanager, AlertTypeSentry, AlertTypeGeneric}

// AlertsPath is the path the receiver takes alert deliveries on, followed by
// /<namespace>/<alertsource> (decision 2).
const AlertsPath = "/alerts/"

// AlertsURL is where the AlertSource namespace/name takes deliveries: base,
// the address its sender reaches the operator at, followed by
// /alerts/<namespace>/<name>.
func AlertsURL(base, namespace, name string) string {
	return strings.TrimRight(base, "/") + AlertsPath + url.PathEscape(namespace) + "/" + url.PathEscape(name)
}

// DefaultAlertContainerLabel is the label, tag or field of an alert that
// names its Container, unless the AlertSource names another (decision 4).
const DefaultAlertContainerLabel = "theos_container"

// DefaultAlertNamespaceLabel is the label of an alert that names the
// namespace its app runs in, unless the AlertSource names another: the one
// Prometheus's Kubernetes metrics carry. A Prometheus that relabels has it
// under another name (exported_namespace, kubernetes_namespace), which is
// its user's to say.
const DefaultAlertNamespaceLabel = "namespace"

// AlertLabels are the names an AlertSource reads an alert by: the label, tag
// or field that names its Container, and the label that names the namespace
// its app runs in. An empty one is its default.
type AlertLabels struct {
	Container, Namespace string
}

func (l AlertLabels) withDefaults() AlertLabels {
	if l.Container == "" {
		l.Container = DefaultAlertContainerLabel
	}
	if l.Namespace == "" {
		l.Namespace = DefaultAlertNamespaceLabel
	}
	return l
}

// MaxAlertsPerDelivery is how many alerts of one delivery are read; the rest
// are counted, never read.
const MaxAlertsPerDelivery = 100

// What an alert may hold. An alert's text comes from a monitoring tool and,
// through it, from whatever the app logged, so every field is cut to a size
// an issue can carry before anything else sees it.
const (
	maxAlertName        = 120
	maxAlertSummary     = 300
	maxAlertDescription = 2000
	maxAlertHint        = 100
	maxAlertPairs       = 40
	maxAlertPairKey     = 80
	maxAlertPairValue   = 500
	maxAlertURL         = 2000
)

// unnamedAlert stands for an alert that says no name.
const unnamedAlert = "unnamed alert"

// Alert is one alert of a delivery, whatever its sender: what fired, since
// when, what it says, the link back, whether it fires or resolved, the key
// that makes two deliveries the same alert, and what names its Container.
// Every text is cut and cleaned by the parser that made it.
type Alert struct {
	// Key makes two deliveries the same alert, within one AlertSource: 24
	// hex digits, a digest of what identifies the alert to its sender.
	Key string
	// Resolved says the sender reports the alert over; false is firing.
	Resolved bool
	// Name is what fired, one line.
	Name string
	// Summary is one line of what it means; Description may be several.
	Summary, Description string
	// StartsAt is since when it fires and EndsAt when it resolved; zero
	// when the sender doesn't say.
	StartsAt, EndsAt time.Time
	// Labels identify the alert and Annotations describe it, as the sender
	// names them. Truncated counts the ones left out.
	Labels, Annotations map[string]string
	Truncated           int
	// URL is the link back to the sender's page: http or https, or empty.
	URL string
	// Container is the Container the alert names itself, by the
	// AlertSource's container label. Namespace is where its app runs and
	// Project the sender's project (Sentry's slug): what leads to a
	// Container when the alert names none.
	Container, Namespace, Project string
}

// alertKey is the key of the alert its sender identifies by parts.
func alertKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%d:%s\x00", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))[:24]
}

// alertLine is s as one line of at most limit characters: valid UTF-8, no
// control or invisible format characters, spaces folded.
func alertLine(s string, limit int) string {
	return cutAlertText(strings.Join(strings.Fields(cleanAlertText(s, false)), " "), limit)
}

// alertBlock is s as text of at most limit characters that keeps its lines.
func alertBlock(s string, limit int) string {
	return cutAlertText(strings.TrimSpace(cleanAlertText(s, true)), limit)
}

// cleanAlertText drops what an alert's text can't be shown with: invalid
// UTF-8, control characters (a newline stays when lines are kept, else it
// becomes a space) and the format characters that hide or reorder text.
func cleanAlertText(s string, lines bool) string {
	s = strings.ToValidUTF8(s, string(utf8.RuneError))
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && lines:
			return r
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
}

// cutAlertText cuts s to limit characters, the last an ellipsis.
func cutAlertText(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	n := 0
	for i := range s {
		if n == limit-1 {
			return strings.TrimSpace(s[:i]) + "…"
		}
		n++
	}
	return s
}

// alertPairs are an alert's labels or annotations as an issue can carry
// them: the first maxAlertPairs by name, each a line, and how many are left
// out.
func alertPairs(pairs map[string]string) (kept map[string]string, dropped int) {
	names := make([]string, 0, len(pairs))
	for name := range pairs {
		names = append(names, name)
	}
	slices.Sort(names)
	kept = map[string]string{}
	for _, name := range names {
		clean := alertLine(name, maxAlertPairKey)
		if _, twice := kept[clean]; clean == "" || twice || len(kept) == maxAlertPairs {
			dropped++
			continue
		}
		kept[clean] = alertLine(pairs[name], maxAlertPairValue)
	}
	if len(kept) == 0 {
		kept = nil
	}
	return kept, dropped
}

// alertURL is s when it is a link an issue may show: an absolute http or
// https address of plain characters. Anything else is no link.
func alertURL(s string) string {
	if s == "" || len(s) > maxAlertURL || strings.ContainsFunc(s, func(r rune) bool {
		return r <= ' ' || r > '~' || strings.ContainsRune("<>\"'`\\^{}|", r)
	}) {
		return ""
	}
	if _, ok := chatLinkURL(s); !ok {
		return ""
	}
	return s
}

// firstAlertURL is the first of candidates that is a link.
func firstAlertURL(candidates ...string) string {
	for _, c := range candidates {
		if u := alertURL(c); u != "" {
			return u
		}
	}
	return ""
}

// AlertRoute is one line of an AlertSource's routes: alerts from Namespace,
// or of the sender's Project, belong to Container. One of the two is set.
type AlertRoute struct {
	Container, Namespace, Project string
}

// AlertPlace is one Container of the AlertSource's namespace and the
// namespaces its deploy targets put it in.
type AlertPlace struct {
	Container  string
	Namespaces []string
}

// AlertContainer is the Container an alert belongs to, or "" and why it
// belongs to none (decision 4). In order: the Container the alert names
// itself, which is never second-guessed; the first route its namespace or
// project matches; the one Container that deploys to its namespace; the
// Container named as its project. places are the namespace's Containers; a
// name that is none of them belongs nowhere.
func AlertContainer(a Alert, routes []AlertRoute, places []AlertPlace) (container, why string) {
	known := func(name string) bool {
		return slices.ContainsFunc(places, func(p AlertPlace) bool { return p.Container == name })
	}
	if a.Container != "" {
		if !known(a.Container) {
			return "", fmt.Sprintf("the alert names Container %q, which this namespace doesn't hold", a.Container)
		}
		return a.Container, ""
	}
	for _, r := range routes {
		if (r.Namespace == "" || r.Namespace != a.Namespace) && (r.Project == "" || r.Project != a.Project) {
			continue
		}
		if !known(r.Container) {
			return "", fmt.Sprintf("a route sends the alert to Container %q, which this namespace doesn't hold",
				r.Container)
		}
		return r.Container, ""
	}
	if a.Namespace != "" {
		var there []string
		for _, p := range places {
			if slices.Contains(p.Namespaces, a.Namespace) {
				there = append(there, p.Container)
			}
		}
		switch {
		case len(there) == 1:
			return there[0], ""
		case len(there) > 1:
			slices.Sort(there)
			return "", fmt.Sprintf("%d Containers deploy to namespace %q (%s): the alert has to name one, or a route",
				len(there), a.Namespace, strings.Join(there, ", "))
		}
	}
	if a.Project != "" && known(a.Project) {
		return a.Project, ""
	}
	var hints []string
	if a.Namespace != "" {
		hints = append(hints, fmt.Sprintf("namespace %q", a.Namespace))
	}
	if a.Project != "" {
		hints = append(hints, fmt.Sprintf("project %q", a.Project))
	}
	if len(hints) == 0 {
		return "", "the alert names no Container, namespace or project"
	}
	return "", "the alert names no Container, and " + strings.Join(hints, " and ") + " leads to none"
}
