package model

import (
	"net/url"
	"strings"
	"time"
)

// EventsFallbackPoll is how long a poll that waits for the Git host waits
// while its GitProvider's deliveries arrive (Events True): a delivery
// reconciles what it concerns at once, and the poll only catches a missed
// one (decision 9, #1222).
const EventsFallbackPoll = 10 * time.Minute

// HostRecheck is when a poll that waits for the Git host looks again: d, or
// EventsFallbackPoll when that is longer and events, its GitProvider's
// deliveries, arrive. Error retries and waits on anything but the host keep
// their intervals and don't use it.
func HostRecheck(d time.Duration, events bool) time.Duration {
	if events {
		return max(d, EventsFallbackPoll)
	}
	return d
}

// The Git host's event hook (docs/plans/git-events.md, decisions 3 and 4).

// GitEventsPath is the path the receiver takes deliveries on, followed by
// /<namespace>/<gitprovider>.
const GitEventsPath = "/git-events/"

// GitEventsSecretKey is the key of a GitProvider's delivery Secret that holds
// the secret its hook signs with.
const GitEventsSecretKey = "secret"

// GitEventsSecretName is the delivery Secret of the GitProvider gitProvider.
func GitEventsSecretName(gitProvider string) string {
	return gitProvider + "-git-events"
}

// GitEventsURL is where the hook of the GitProvider namespace/name delivers:
// base, the address the host reaches the operator at, followed by
// /git-events/<namespace>/<name>. The path names the GitProvider whose
// Secret checks the delivery, and is how the host's hook is found again.
func GitEventsURL(base, namespace, name string) string {
	return strings.TrimRight(base, "/") + GitEventsPath + url.PathEscape(namespace) + "/" + url.PathEscape(name)
}
