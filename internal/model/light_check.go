package model

import (
	"bufio"
	"bytes"
	"slices"
	"strings"
)

// LightCheckFile is the file CheckLightCommand runs a target of.
const LightCheckFile = "Makefile"

// lightCheckTarget is the target of LightCheckFile that CheckLightCommand runs.
const lightCheckTarget = "check-light"

// HasLightCheck reports whether makefile defines the target CheckLightCommand
// runs. A repository without one fails every change's check once its checks
// are BeforeRelease, so the switch waits for it.
func HasLightCheck(makefile []byte) bool {
	return slices.Contains(MakeTargets(makefile), lightCheckTarget)
}

// MakeTargets returns the targets makefile's rules name, in order, as many
// times as a rule names them: each line outside a recipe whose first colon
// isn't part of an assignment. Special targets such as .PHONY count too; only
// a rule's own line counts, not what an include or a variable adds.
func MakeTargets(makefile []byte) []string {
	var targets []string
	scanner := bufio.NewScanner(bytes.NewReader(makefile))
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "\t") {
			continue // a recipe line
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		names, rest, ok := strings.Cut(line, ":")
		if !ok || strings.ContainsAny(names, "=") || strings.HasPrefix(rest, "=") ||
			strings.HasPrefix(rest, ":=") || strings.HasPrefix(rest, "::=") {
			continue // no rule, or an assignment such as X := a or X = a:b
		}
		targets = append(targets, strings.Fields(names)...)
	}
	return targets
}
