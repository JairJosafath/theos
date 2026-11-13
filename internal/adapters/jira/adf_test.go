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
	"encoding/json"
	"testing"
)

// TestFlattenADF covers #157: a description in Atlassian's document format
// is flattened to text by a pure function that never fails.
func TestFlattenADF(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"no description", `null`, ""},
		{"an empty document", `{"type":"doc","version":1,"content":[]}`, ""},
		{"a string Jira gave as plain text", `"Dark mode, please."`, "Dark mode, please."},
		{"a document that is not one", `[1,2]`, ""},
		{"a paragraph with marks", `{"type":"doc","content":[{"type":"paragraph","content":[
			{"type":"text","text":"Add "},{"type":"text","text":"dark","marks":[{"type":"strong"}]},
			{"type":"text","text":" mode."}]}]}`, "Add dark mode."},
		{"two paragraphs are two lines", `{"type":"doc","content":[
			{"type":"paragraph","content":[{"type":"text","text":"One."}]},
			{"type":"paragraph","content":[{"type":"text","text":"Two."}]}]}`, "One.\nTwo."},
		{"an empty paragraph is an empty line", `{"type":"doc","content":[
			{"type":"paragraph","content":[{"type":"text","text":"One."}]},{"type":"paragraph"},
			{"type":"paragraph","content":[{"type":"text","text":"Two."}]}]}`, "One.\n\nTwo."},
		{"a hard break is a line break", `{"type":"doc","content":[{"type":"paragraph","content":[
			{"type":"text","text":"a"},{"type":"hardBreak"},{"type":"text","text":"b"}]}]}`, "a\nb"},
		{"a heading", `{"type":"doc","content":[{"type":"heading","attrs":{"level":2},
			"content":[{"type":"text","text":"Why"}]}]}`, "Why"},
		{"a link shows its address", `{"type":"doc","content":[{"type":"paragraph","content":[
			{"type":"text","text":"the spec","marks":[{"type":"link","attrs":{"href":"https://x.test/spec"}}]}]}]}`,
			"the spec (https://x.test/spec)"},
		{"a link that is its own text shows it once", `{"type":"doc","content":[{"type":"paragraph","content":[
			{"type":"text","text":"https://x.test","marks":[{"type":"link","attrs":{"href":"https://x.test"}}]}]}]}`,
			"https://x.test"},
		{"a bullet list and an ordered list", `{"type":"doc","content":[
			{"type":"bulletList","content":[
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]},
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}]},
			{"type":"orderedList","attrs":{"order":3},"content":[
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"c"}]}]}]}]}`,
			"- a\n- b\n3. c"},
		{"a list inside a list is indented under its item", `{"type":"doc","content":[{"type":"bulletList","content":[
			{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]},
				{"type":"bulletList","content":[{"type":"listItem","content":[
					{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}]}]}]}]}`, "- a\n  - b"},
		{"a code block", `{"type":"doc","content":[{"type":"codeBlock","attrs":{"language":"go"},
			"content":[{"type":"text","text":"x := 1\ny := 2"}]}]}`, "x := 1\ny := 2"},
		{"a quote", `{"type":"doc","content":[{"type":"blockquote","content":[
			{"type":"paragraph","content":[{"type":"text","text":"said"}]}]}]}`, "> said"},
		{"a mention, an emoji, a card, a status and a date", `{"type":"doc","content":[{"type":"paragraph","content":[
			{"type":"mention","attrs":{"id":"abc","text":"@Ada"}},{"type":"text","text":" "},
			{"type":"emoji","attrs":{"shortName":":smile:","text":"😄"}},{"type":"text","text":" "},
			{"type":"inlineCard","attrs":{"url":"https://x.test/c"}},{"type":"text","text":" "},
			{"type":"status","attrs":{"text":"READY","color":"green"}},{"type":"text","text":" "},
			{"type":"date","attrs":{"timestamp":"1767344400000"}}]}]}`, "@Ada 😄 https://x.test/c READY 2026-01-02"},
		{"a table is a line to a row", `{"type":"doc","content":[{"type":"table","content":[
			{"type":"tableRow","content":[
				{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]},
				{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}]},
			{"type":"tableRow","content":[
				{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"1"}]}]},
				{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"2"}]}]}]}]}]}`,
			"a | b\n1 | 2"},
		{"a panel holds blocks", `{"type":"doc","content":[{"type":"panel","attrs":{"panelType":"info"},"content":[
			{"type":"paragraph","content":[{"type":"text","text":"note"}]}]}]}`, "note"},
		{"an image has no text and is left out", `{"type":"doc","content":[{"type":"mediaSingle","content":[
			{"type":"media","attrs":{"id":"1","type":"file"}}]},
			{"type":"paragraph","content":[{"type":"text","text":"after"}]}]}`, "after"},
		{"a node Jira adds later is read for its text", `{"type":"doc","content":[{"type":"newThing","content":[
			{"type":"paragraph","content":[{"type":"text","text":"still here"}]}]}]}`, "still here"},
		{"a rule", `{"type":"doc","content":[{"type":"rule"}]}`, "---"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FlattenADF(json.RawMessage(tt.doc)); got != tt.want {
				t.Errorf("FlattenADF() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := FlattenADF(nil); got != "" {
		t.Errorf("FlattenADF(nil) = %q, want an empty field", got)
	}
}

// TestADFDocumentRoundTrips: a comment is written one paragraph to a line,
// and reads back as it was written, empty lines included.
func TestADFDocumentRoundTrips(t *testing.T) {
	for _, text := range []string{"", "one", "one\ntwo", "Planned as: dark mode\n\n<!-- marker -->", "a\n\n\nb", "trailing\n"} {
		raw, err := json.Marshal(adfDocument(text))
		if err != nil {
			t.Fatal(err)
		}
		if got := FlattenADF(raw); got != text {
			t.Errorf("FlattenADF(adfDocument(%q)) = %q", text, got)
		}
	}
}
