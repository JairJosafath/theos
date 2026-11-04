package model

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// The fence: the rules that keep agents inside their Container and off the
// cluster (docs/design.md, "The fence" and "The CI fence"). The paths no
// agent may change, the operator's own files, and the private address
// ranges HTTPS egress never reaches live here, so one file guards them
// (docs/plans/self-hosting.md, step 2).

// AgentNeverPaths are the protected paths no agent may change, even through a
// Standard change: the agents' own rules and the platform's contract.
var AgentNeverPaths = []string{"AGENTS.md", "docs/agents/", ".theos.yaml"}

// OperatorPaths are the operator's own files in every Container, whatever its
// CI system: the identity file and the dev container.
var OperatorPaths = []string{".theos.yaml", ".devcontainer/"}

// AgentProtectedPaths returns the paths agents may not change in a Container:
// the platform's for its CI system and its Standard's, sorted and without
// duplicates.
func AgentProtectedPaths(ci CI, standard []string) []string {
	return sortedPaths(PlatformProtectedPaths(ci), standard)
}

// PrivateRanges are the addresses the fences keep agents and CI jobs from over
// HTTPS: the cluster's pods, services and nodes, and anything else on a
// private network (docs/design.md, "The fence" and "The CI fence").
func PrivateRanges() []string {
	return []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
}

// PrivateBlock reports whether cidr is an address block (10.96.0.10/32,
// 169.254.20.10/32) that lies inside one of PrivateRanges: an address the
// cluster's own services answer on, which is where cluster DNS runs (#1257).
func PrivateBlock(cidr string) bool {
	block, err := netip.ParsePrefix(cidr)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(PrivateRanges(), func(r string) bool {
		private := netip.MustParsePrefix(r)
		return private.Bits() <= block.Bits() && private.Contains(block.Addr())
	})
}

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
// as a word (AGENTS.md, docs/agents/reviewer.md, .theos.yaml, with or
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
