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
	"html"
	"net/url"
	"strings"
)

// ChatLink is a link in a chat message: to the UI's object page, the Git
// host's pull request or issue.
type ChatLink struct {
	Text string
	URL  string
}

// ChatPost is a message theos-chat posts (docs/plans/done/chat-matrix.md,
// "Formatting"): a bold title, a status line, the words of whoever wrote
// them (an issue's question, a review's verdict), links, and lines to copy
// such as kubectl commands. Text and HTML render it for Matrix's body and
// formatted_body; every field is shown as it is, never read as markup.
type ChatPost struct {
	Title  string
	Status string
	// Body are paragraphs, their line breaks kept.
	Body  []string
	Links []ChatLink
	// Code are lines to copy, shown as one code block.
	Code []string
}

// Text is the post as plain text, Matrix's body.
func (p ChatPost) Text() string {
	var blocks []string
	if head := nonEmpty(p.Title, p.Status); len(head) > 0 {
		blocks = append(blocks, strings.Join(head, "\n"))
	}
	blocks = append(blocks, nonEmpty(p.Body...)...)
	var links []string
	for _, l := range p.Links {
		if u, ok := chatLinkURL(l.URL); ok {
			links = append(links, chatLinkText(l)+": "+u)
		}
	}
	if len(links) > 0 {
		blocks = append(blocks, strings.Join(links, "\n"))
	}
	if code := nonEmpty(p.Code...); len(code) > 0 {
		blocks = append(blocks, strings.Join(code, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

// HTML is the post in Matrix's HTML subset (org.matrix.custom.html), every
// field escaped, a link only when its address is http or https.
func (p ChatPost) HTML() string {
	var b strings.Builder
	switch title, status := strings.TrimSpace(p.Title), strings.TrimSpace(p.Status); {
	case title != "" && status != "":
		b.WriteString("<p><b>" + chatEscape(title) + "</b><br>" + chatEscape(status) + "</p>")
	case title != "":
		b.WriteString("<p><b>" + chatEscape(title) + "</b></p>")
	case status != "":
		b.WriteString("<p>" + chatEscape(status) + "</p>")
	}
	for _, para := range nonEmpty(p.Body...) {
		b.WriteString("<p>" + chatEscape(para) + "</p>")
	}
	var links []string
	for _, l := range p.Links {
		if u, ok := chatLinkURL(l.URL); ok {
			links = append(links, `<a href="`+html.EscapeString(u)+`">`+chatEscape(chatLinkText(l))+"</a>")
		}
	}
	if len(links) > 0 {
		b.WriteString("<p>" + strings.Join(links, " · ") + "</p>")
	}
	if code := nonEmpty(p.Code...); len(code) > 0 {
		b.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>")
	}
	return b.String()
}

// chatEscape escapes text for HTML and keeps its line breaks.
func chatEscape(text string) string {
	return strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
}

// chatLinkURL is raw when it is an absolute http or https address with a
// host and nothing a client could read otherwise; a javascript: or data:
// link, or one with spaces or control characters, is dropped.
func chatLinkURL(raw string) (string, bool) {
	if raw == "" || strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	return raw, true
}

// chatLinkText is the link's words, or its address when it has none.
func chatLinkText(l ChatLink) string {
	if t := strings.TrimSpace(l.Text); t != "" {
		return t
	}
	return l.URL
}

// nonEmpty returns the strings that aren't blank, trimmed.
func nonEmpty(items ...string) []string {
	var out []string
	for _, s := range items {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
