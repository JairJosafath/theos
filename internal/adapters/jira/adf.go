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
	"slices"
	"strconv"
	"strings"
	"time"
)

// The names that more than one place spells.
const (
	nodeOrderedList = "orderedList"
	nodeDate        = "date"
	nodeEmoji       = "emoji"
	nodeText        = "text"
	keyText         = "text"
	nodeHardBreak   = "hardBreak"
	keyType         = "type"
)

// node is one node of Atlassian's document format: a document, a block such
// as a paragraph or a list, or an inline such as text. Only what flattening
// reads is decoded.
type node struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Content []node          `json:"content"`
	Attrs   json.RawMessage `json:"attrs"`
	Marks   []mark          `json:"marks"`
}

type mark struct {
	Type  string          `json:"type"`
	Attrs json.RawMessage `json:"attrs"`
}

// attr is the string value of attribute name of a node's or mark's attrs;
// a number is written as it is, and anything else is "".
func attr(attrs json.RawMessage, name string) string {
	var all map[string]json.RawMessage
	if json.Unmarshal(attrs, &all) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(all[name], &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(all[name], &n) == nil {
		return n.String()
	}
	return ""
}

// FlattenADF is text of a Jira field in Atlassian's document format: the
// blocks one to a line, a list's items one to a line behind "- " or "1. ",
// a hard break a line break, a link as "text (address)", and the rest of the
// document, which has no text (an image, a status's colour), left out. A
// string that is no document, a field Jira gave as plain text, is returned
// as it is, and an empty field is "". It is a pure function, and never fails:
// a node it does not know is read for the text inside it.
func FlattenADF(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	var doc node
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	return strings.Join(blocks(doc.Content, ""), "\n")
}

// blocks are the lines of the blocks in nodes, each behind prefix.
func blocks(nodes []node, prefix string) []string {
	lines := make([]string, 0, len(nodes))
	for _, n := range nodes {
		lines = append(lines, block(n, prefix)...)
	}
	return lines
}

// block is the lines of one block n, each behind prefix.
func block(n node, prefix string) []string {
	switch n.Type {
	case "bulletList", nodeOrderedList:
		return list(n, prefix)
	case "blockquote":
		return blocks(n.Content, prefix+"> ")
	case "table":
		var lines []string
		for _, row := range n.Content {
			cells := make([]string, 0, len(row.Content))
			for _, cell := range row.Content {
				cells = append(cells, strings.Join(blocks(cell.Content, ""), " "))
			}
			lines = append(lines, prefix+strings.Join(cells, " | "))
		}
		return lines
	case "rule":
		return []string{prefix + "---"}
	case "paragraph", "heading", "codeBlock":
		// One line of text, however many breaks are in it.
		return lines(inline(n), prefix)
	}
	if inlineNodes[n.Type] {
		return lines(inline(n), prefix)
	}
	if hasBlocks(n.Content) {
		// A panel, an expand, a layout and what Jira adds later hold blocks.
		return blocks(n.Content, prefix)
	}
	if text := inline(n); text != "" {
		return lines(text, prefix)
	}
	// A block with no text, such as an image, leaves no line.
	return nil
}

// hasBlocks says some node of nodes is not an inline.
func hasBlocks(nodes []node) bool {
	return slices.ContainsFunc(nodes, func(n node) bool { return !inlineNodes[n.Type] })
}

// inlineNodes are the types of the nodes that hold text in a line.
var inlineNodes = map[string]bool{nodeText: true, nodeHardBreak: true, "mention": true, nodeEmoji: true,
	"inlineCard": true, "status": true, nodeDate: true}

// lines splits text at its line breaks, each line behind prefix.
func lines(text, prefix string) []string {
	parts := strings.Split(text, "\n")
	for i := range parts {
		parts[i] = prefix + parts[i]
	}
	return parts
}

// list is the lines of a bullet or an ordered list: an item's first line
// behind its marker, the rest, and a list inside it, indented under it.
func list(n node, prefix string) []string {
	var out []string
	first := 1
	if n.Type == nodeOrderedList {
		if v, err := strconv.Atoi(attr(n.Attrs, "order")); err == nil {
			first = v
		}
	}
	for i, item := range n.Content {
		marker := "- "
		if n.Type == nodeOrderedList {
			marker = strconv.Itoa(first+i) + ". "
		}
		inner := blocks(item.Content, "")
		for j, line := range inner {
			if j == 0 {
				out = append(out, prefix+marker+line)
			} else {
				out = append(out, prefix+strings.Repeat(" ", len(marker))+line)
			}
		}
		if len(inner) == 0 {
			out = append(out, prefix+strings.TrimRight(marker, " "))
		}
	}
	return out
}

// inline is the text of n and what is inside it, on as many lines as it
// has hard breaks.
func inline(n node) string {
	switch n.Type {
	case nodeText:
		return linked(n)
	case nodeHardBreak:
		return "\n"
	case "mention":
		return attr(n.Attrs, keyText)
	case nodeEmoji:
		if t := attr(n.Attrs, keyText); t != "" {
			return t
		}
		return attr(n.Attrs, "shortName")
	case "inlineCard":
		return attr(n.Attrs, "url")
	case "status":
		return attr(n.Attrs, keyText)
	case nodeDate:
		ms, err := strconv.ParseInt(attr(n.Attrs, "timestamp"), 10, 64)
		if err != nil {
			return ""
		}
		return time.UnixMilli(ms).UTC().Format(time.DateOnly)
	}
	if n.Text != "" {
		return n.Text
	}
	var b strings.Builder
	for _, c := range n.Content {
		b.WriteString(inline(c))
	}
	return b.String()
}

// linked is a text node's text, with the address of its link after it when
// the text isn't the address.
func linked(n node) string {
	for _, m := range n.Marks {
		if m.Type != "link" {
			continue
		}
		if href := attr(m.Attrs, "href"); href != "" && href != n.Text {
			return n.Text + " (" + href + ")"
		}
	}
	return n.Text
}

// adfDocument is text as a document: one paragraph to a line, an empty
// paragraph for an empty line, so that FlattenADF reads back what was
// written. A comment is written in it, since Jira's version 3 takes no other.
func adfDocument(text string) map[string]any {
	var content []map[string]any
	for line := range strings.SplitSeq(text, "\n") {
		p := map[string]any{keyType: "paragraph"}
		if line != "" {
			p["content"] = []map[string]any{{keyType: nodeText, keyText: line}}
		}
		content = append(content, p)
	}
	return map[string]any{"version": 1, keyType: "doc", "content": content}
}
