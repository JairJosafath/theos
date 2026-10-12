package model

import (
	"fmt"
	"slices"
	"strings"
)

// neverPathsCode is AgentNeverPaths as prose, each path in backticks.
func neverPathsCode() string {
	quoted := make([]string, len(AgentNeverPaths))
	for i, p := range AgentNeverPaths {
		quoted[i] = "`" + p + "`"
	}
	return joinAnd(quoted)
}

// renderNeverPaths is brief.md's section on the paths no issue may change
// (docs/plans/done/out-of-reach-asks.md, decision 2).
func renderNeverPaths() string {
	return fmt.Sprintf("\n## What no issue can change\n\n%s are the agents' rules and the platform's settings: "+
		"only the Standard's own release or the operator changes them, never a pull request. So no issue asks "+
		"to change them, and none names them in its title or acceptance criteria. When a **new** prompt or "+
		"request asks for such a change, file no issue for it: say what wasn't planned in `%s` and why, and "+
		"where it can go instead, in `%s`; skip such a request saying why. Notes for the agents go in `%s`, "+
		"which every agent reads and any issue may write.\n",
		neverPathsCode(), PlanSummaryFile, PlanChoicesFile, ProjectNotesPath)
}

// NeverPathNamed returns the first path under AgentNeverPaths that text names
// as a word (AGENTS.md, docs/agents/reviewer.md, .c4factory.yaml, with or
// without backticks), as written, or "" when it names none. A file of the
// same name elsewhere (pkg/AGENTS.md) is not under them.
func NeverPathNamed(text string) string {
	if spans := neverPathSpans(text); len(spans) > 0 {
		return text[spans[0][0]:spans[0][1]]
	}
	return ""
}

// withoutNeverPaths is text with each path under AgentNeverPaths it names
// put as "a protected file", so a stand-in title asks for no such change.
func withoutNeverPaths(text string) string {
	spans := neverPathSpans(text)
	for _, s := range slices.Backward(spans) {
		start, end := s[0], s[1]
		if start > 0 && end < len(text) && text[start-1] == '`' && text[end] == '`' {
			start, end = start-1, end+1
		}
		text = text[:start] + "a protected file" + text[end:]
	}
	return text
}

// neverPathSpans finds each path under AgentNeverPaths that text names as a
// word, ordered by where it starts, with the "./" written before it; a
// directory's span runs to the end of the path written under it.
func neverPathSpans(text string) [][2]int {
	var spans [][2]int
	for _, p := range AgentNeverPaths {
		base := strings.TrimSuffix(p, "/")
		dir := base != p
		for from := 0; ; {
			i := strings.Index(text[from:], base)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(base)
			from = start + 1
			left := start
			if left >= 2 && text[left-2:left] == "./" {
				left -= 2
			}
			if left > 0 && isPathByte(text[left-1]) {
				continue
			}
			if dir && end < len(text) && text[end] == '/' {
				for end < len(text) && isPathByte(text[end]) {
					end++
				}
				for text[end-1] == '.' {
					end--
				}
			} else if !wordEnds(text[end:]) {
				continue
			}
			spans = append(spans, [2]int{left, end})
		}
	}
	slices.SortFunc(spans, func(a, b [2]int) int { return a[0] - b[0] })
	return spans
}

// wordEnds says whether a path ends where rest starts: at the end, before a
// byte no path holds, or before dots that end a sentence ("AGENTS.md...").
func wordEnds(rest string) bool {
	rest = strings.TrimLeft(rest, ".")
	return rest == "" || !isPathByte(rest[0])
}

// isPathByte says whether c may stand inside a path.
func isPathByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '.' || c == '/'
}
