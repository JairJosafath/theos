package model

import (
	"errors"
	"net/url"
	"strings"
)

// theos-chat's posts link to the object's page in the UI
// (docs/plans/done/chat-ui-links.md), where the UI's address is a setting:
// theos-chat's --ui-url, the chart's chat.uiURL. Without one, a post links to
// the Git host alone, as before.

// ChatUIURL checks raw, the UI's address for theos-chat's links: "" for
// none, else an absolute http or https address with a host and no user,
// query or fragment, which it returns without its trailing slashes.
func ChatUIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return "", err
	case u.Scheme != "http" && u.Scheme != "https":
		return "", errors.New("the UI's address must be http or https")
	case u.Host == "" || u.Opaque != "":
		return "", errors.New("the UI's address must name a host")
	case u.User != nil:
		return "", errors.New("the UI's address must carry no user")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "", errors.New("the UI's address must carry no query or fragment: theos-chat adds the page's")
	}
	return strings.TrimRight(raw, "/"), nil
}

// ChatPageURL is the address of the UI's page of the object of kind and name
// in namespace, as the UI reads it (ui/src/lib/hooks.ts, parseUrl:
// ?ns=&kind=&name=), under base, ChatUIURL's; "" without a base or a name.
func ChatPageURL(base, namespace, kind, name string) string {
	if base == "" || kind == "" || name == "" {
		return ""
	}
	q := url.Values{"kind": {kind}, "name": {name}}
	if namespace != "" {
		q.Set("ns", namespace)
	}
	return base + "/?" + q.Encode()
}

// chatPageLink is the link to an object's page in the UI, none without its
// address.
func chatPageLink(kind, pageURL string) []ChatLink {
	if pageURL == "" {
		return nil
	}
	return []ChatLink{{Text: kind + " in theos", URL: pageURL}}
}
