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

package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/JairJosafath/theos/internal/ports"
)

// maxResponse bounds what one answer may hold.
const maxResponse = 16 << 20

// maxMessage is the most of Linear's own words an error's message repeats.
const maxMessage = 200

type graphqlRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables,omitempty"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphqlError  `json:"errors"`
}

// graphqlError is one error of Linear's answer. What its extensions hold is
// from memory of Linear's answers: its reference lists no codes.
type graphqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Type                   string `json:"type"`
		Code                   string `json:"code"`
		UserPresentableMessage string `json:"userPresentableMessage"`
	} `json:"extensions"`
}

// call sends the operation op, whose text is query, and decodes its data
// into out. what says it in an error. notFound is the reason of an answer
// that says an entity isn't there: IssueNotFound for a call on an issue,
// TrackerNotFound for the team's list.
func (l *linear) call(ctx context.Context, what, op, query string, vars map[string]any, notFound string,
	out any) error {
	payload, err := json.Marshal(graphqlRequest{Query: query, OperationName: op, Variables: vars})
	if err != nil {
		return &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: "linear: " + err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.api, bytes.NewReader(payload))
	if err != nil {
		return &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: "linear: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// An API key is sent as it is, with no scheme before it; only an OAuth
	// access token is a Bearer (Linear's GraphQL page).
	req.Header.Set("Authorization", l.token)
	resp, err := l.http.Do(req)
	if err != nil {
		return &ports.IssuesError{Reason: ports.ReasonTrackerUnavailable,
			Message: fmt.Sprintf("linear: %s: %v", what, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return &ports.IssuesError{Reason: ports.ReasonTrackerUnavailable,
			Message: fmt.Sprintf("linear: %s: %v", what, err)}
	}
	var answer graphqlResponse
	// An answer that isn't JSON has no errors to read: its status speaks.
	_ = json.Unmarshal(data, &answer)
	if len(answer.Errors) > 0 || resp.StatusCode != http.StatusOK {
		return failure(what, resp.StatusCode, answer.Errors, notFound)
	}
	if len(answer.Data) == 0 || string(answer.Data) == "null" || json.Unmarshal(answer.Data, out) != nil {
		return &ports.IssuesError{Reason: ports.ReasonTrackerError, Status: resp.StatusCode,
			Message: fmt.Sprintf("linear: %s: unreadable response", what)}
	}
	return nil
}

// failure is the IssuesError of an answer that failed. A call needs all of
// what it asked for, so an answer with errors fails even on a 200, where
// Linear may send part of the data beside them. The extensions decide, the
// status only where they say nothing.
func failure(what string, status int, errs []graphqlError, notFound string) *ports.IssuesError {
	reason, message := "", ""
	if len(errs) > 0 {
		message = errs[0].Message
	}
	// Who may not, and what to wait out, outrank what is not there.
	rank := map[string]int{ports.ReasonUnauthorized: 4, ports.ReasonTokenLacksIssues: 3,
		ports.ReasonTrackerUnavailable: 2, notFound: 1}
	for _, e := range errs {
		code, kind := strings.ToUpper(e.Extensions.Code), strings.ToLower(e.Extensions.Type)
		msg := strings.ToLower(e.Message + " " + e.Extensions.UserPresentableMessage)
		var r string
		switch {
		case code == "AUTHENTICATION_ERROR" || kind == "authentication error":
			r = ports.ReasonUnauthorized
		case code == "FORBIDDEN" || kind == "forbidden":
			r = ports.ReasonTokenLacksIssues
		case code == "RATELIMITED" || kind == "ratelimited":
			r = ports.ReasonTrackerUnavailable
		case strings.Contains(msg, "not found") || strings.Contains(msg, "could not find"):
			r = notFound
		}
		if rank[r] > rank[reason] {
			reason, message = r, e.Message
		}
	}
	if reason == "" {
		switch {
		case status == http.StatusUnauthorized:
			reason = ports.ReasonUnauthorized
		case status == http.StatusForbidden:
			reason = ports.ReasonTokenLacksIssues
		case status == http.StatusTooManyRequests || status >= http.StatusInternalServerError:
			reason = ports.ReasonTrackerUnavailable
		default:
			reason = ports.ReasonTrackerError
		}
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return &ports.IssuesError{Reason: reason, Status: status,
		Message: fmt.Sprintf("linear: %s failed: %s", what, oneLine(message, maxMessage))}
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
