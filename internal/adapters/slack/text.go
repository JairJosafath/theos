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

package slack

import "strings"

// unescaper turns Slack's three entities back into their characters, in
// one pass, so that &amp;lt; is &lt; and not <.
var unescaper = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")

// Decode is the text of a message as its writer typed it, from the text
// Slack sends (docs/plans/requests-and-chat.md, decision 6): a link
// <address|words> becomes its address, with its words before it when the
// two differ; a mention <@U…> or <@U…|name> becomes @ and the member ID,
// which is what a sender is named by; a channel <#C…|name> becomes # and
// its name; a special mention <!here> becomes @here; and the entities
// &amp;, &lt; and &gt; become their characters, last, so that an entity
// typed in the text never makes a link or a mention.
func Decode(text string) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(text, '<')
		if open < 0 {
			break
		}
		end := strings.IndexByte(text[open:], '>')
		if end < 0 {
			break
		}
		b.WriteString(unescaper.Replace(text[:open]))
		b.WriteString(decodeAngle(text[open+1 : open+end]))
		text = text[open+end+1:]
	}
	b.WriteString(unescaper.Replace(text))
	return b.String()
}

// decodeAngle is what one <…> of Slack's text stands for.
func decodeAngle(inner string) string {
	target, label, labelled := strings.Cut(inner, "|")
	target, label = unescaper.Replace(target), unescaper.Replace(label)
	switch {
	case strings.HasPrefix(target, "@"):
		return target
	case strings.HasPrefix(target, "#"):
		if labelled && label != "" {
			return "#" + label
		}
		return target
	case strings.HasPrefix(target, "!"):
		if labelled && label != "" {
			return label
		}
		name, _, _ := strings.Cut(target[1:], "^")
		return "@" + name
	}
	if address, ok := strings.CutPrefix(target, "mailto:"); ok && label == address {
		return address
	}
	if label == "" || label == target {
		return target
	}
	return label + " " + target
}
