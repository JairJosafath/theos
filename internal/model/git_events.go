package model

import (
	"net/url"
	"strings"
)

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
