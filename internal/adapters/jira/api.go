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

package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/JairJosafath/theos/internal/ports"
)

// maxResponse bounds what one answer may hold.
const maxResponse = 16 << 20

// maxMessage is the most of Jira's own words an error's message repeats.
const maxMessage = 200

// get reads path with query into out.
func (j *jira) get(ctx context.Context, what, path string, query url.Values, notFound string, out any) error {
	status, data, err := j.do(ctx, what, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	return j.read(what, status, data, notFound, out)
}

// send sends body to path with method, and reads the answer into out when
// there is one to read.
func (j *jira) send(ctx context.Context, what, method, path string, body any, notFound string, out any) error {
	status, data, err := j.do(ctx, what, method, path, nil, body)
	if err != nil {
		return err
	}
	return j.read(what, status, data, notFound, out)
}

// read is the result of an answer: an error when it failed, else its body
// decoded into out.
func (j *jira) read(what string, status int, data []byte, notFound string, out any) error {
	if status < 200 || status > 299 {
		return failure(what, status, data, notFound)
	}
	if out == nil || status == http.StatusNoContent {
		return nil
	}
	if json.Unmarshal(data, out) != nil {
		return &ports.IssuesError{Reason: ports.ReasonTrackerError, Status: status,
			Message: fmt.Sprintf("jira: %s: unreadable response", what)}
	}
	return nil
}

// do makes one call, as the credential's account: HTTP basic, the account's
// e-mail address and the API token (Atlassian's page on API tokens). A
// network failure is the only error; whatever Jira answers is returned.
func (j *jira) do(ctx context.Context, what, method, path string, query url.Values, body any) (int, []byte, error) {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: "jira: " + err.Error()}
		}
		payload = bytes.NewReader(b)
	}
	target := j.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return 0, nil, &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: "jira: " + err.Error()}
	}
	req.SetBasicAuth(j.email, j.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := j.http.Do(req)
	if err != nil {
		return 0, nil, &ports.IssuesError{Reason: ports.ReasonTrackerUnavailable,
			Message: fmt.Sprintf("jira: %s: %v", what, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return 0, nil, &ports.IssuesError{Reason: ports.ReasonTrackerUnavailable,
			Message: fmt.Sprintf("jira: %s: %v", what, err)}
	}
	return resp.StatusCode, data, nil
}

// words is what Jira says of a failure: the messages of its error body,
// whichever of its two shapes it has (the REST API's errorMessages and
// errors, the gateway's message).
func words(data []byte) string {
	var body struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
		Message       string            `json:"message"`
	}
	_ = json.Unmarshal(data, &body)
	parts := slices.Clone(body.ErrorMessages)
	for _, key := range slices.Sorted(maps.Keys(body.Errors)) {
		parts = append(parts, body.Errors[key])
	}
	return strings.TrimSpace(strings.Join(append(parts, body.Message), " "))
}

// failure is the IssuesError of an answer that failed. notFound is the
// reason of a 404: IssueNotFound for a call on an item, TrackerNotFound for
// the project's list. The reference lists 400, 401 and 404 and no 403;
// what is mapped past them is the HTTP status's usual meaning.
func failure(what string, status int, data []byte, notFound string) *ports.IssuesError {
	said := words(data)
	var reason string
	switch {
	case status == http.StatusUnauthorized && strings.Contains(strings.ToLower(said), "scope"):
		// A scoped token that lacks a scope of the call (the gateway's
		// words, from memory: the reference says only that the credentials
		// are incorrect).
		reason = ports.ReasonTokenLacksIssues
	case status == http.StatusUnauthorized:
		reason = ports.ReasonUnauthorized
	case status == http.StatusForbidden:
		reason = ports.ReasonTokenLacksIssues
	case status == http.StatusNotFound:
		reason = notFound
	case status == http.StatusBadRequest && notFound == ports.ReasonTrackerNotFound &&
		strings.Contains(strings.ToLower(said), "does not exist for the field 'project'"):
		// A search of a project the account can't see, or that isn't there,
		// is a 400 that says so.
		reason = ports.ReasonTrackerNotFound
	case status == http.StatusTooManyRequests || status >= http.StatusInternalServerError ||
		status == http.StatusConflict:
		reason = ports.ReasonTrackerUnavailable
	default:
		reason = ports.ReasonTrackerError
	}
	if said == "" {
		said = http.StatusText(status)
	}
	return &ports.IssuesError{Reason: reason, Status: status,
		Message: fmt.Sprintf("jira: %s failed: %s", what, oneLine(said, maxMessage))}
}

// oneLine is s on one line of at most limit characters: every run of
// whitespace and control characters is one space, and what is left is cut
// at the limit.
func oneLine(s string, limit int) string {
	var b strings.Builder
	n, space := 0, false
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = b.Len() > 0
			continue
		}
		width := 1
		if space {
			width = 2
		}
		if n+width > limit {
			break
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
		n += width
	}
	return b.String()
}
