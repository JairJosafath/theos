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

package packages

import (
	"regexp"
	"strconv"
	"strings"
)

// pep440 matches a public or local version as PEP 440 appendix B spells it,
// in any of its permitted forms: a leading v, any separator before a pre,
// post or dev segment, and alpha, beta, c, pre, preview, rev and r.
var pep440 = regexp.MustCompile(`(?i)^v?(?:(?P<epoch>[0-9]+)!)?(?P<release>[0-9]+(?:\.[0-9]+)*)` +
	`(?P<pre>[-_.]?(?P<preL>a|b|c|rc|alpha|beta|pre|preview)[-_.]?(?P<preN>[0-9]+)?)?` +
	`(?P<post>(?:-(?P<postN1>[0-9]+))|(?:[-_.]?(?P<postL>post|rev|r)[-_.]?(?P<postN2>[0-9]+)?))?` +
	`(?P<dev>[-_.]?(?P<devL>dev)[-_.]?(?P<devN>[0-9]+)?)?` +
	`(?:\+(?P<local>[a-z0-9]+(?:[-_.][a-z0-9]+)*))?$`)

// normalizePEP440 returns v in PEP 440's normalised form, the one an index
// files a version under: 1.0.0rc1 for v1.0.0-rc.1, 1.0.post1 for 1.0-1. ok is
// false when v is no PEP 440 version.
func normalizePEP440(v string) (string, bool) {
	m := pep440.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "", false
	}
	group := func(name string) string { return m[pep440.SubexpIndex(name)] }
	var b strings.Builder
	if epoch := group("epoch"); epoch != "" && trimZeros(epoch) != "0" {
		b.WriteString(trimZeros(epoch) + "!")
	}
	segments := strings.Split(group("release"), ".")
	for i, s := range segments {
		segments[i] = trimZeros(s)
	}
	b.WriteString(strings.Join(segments, "."))
	if group("pre") != "" {
		switch l := strings.ToLower(group("preL")); l {
		case "alpha":
			b.WriteString("a")
		case "beta":
			b.WriteString("b")
		case "c", "pre", "preview":
			b.WriteString("rc")
		default:
			b.WriteString(l)
		}
		b.WriteString(trimZeros(group("preN")))
	}
	if group("post") != "" {
		b.WriteString(".post" + trimZeros(group("postN1")+group("postN2")))
	}
	if group("dev") != "" {
		b.WriteString(".dev" + trimZeros(group("devN")))
	}
	if local := group("local"); local != "" {
		parts := strings.FieldsFunc(strings.ToLower(local), func(r rune) bool { return r == '-' || r == '_' || r == '.' })
		for i, p := range parts {
			if _, err := strconv.Atoi(p); err == nil {
				parts[i] = trimZeros(p)
			}
		}
		b.WriteString("+" + strings.Join(parts, "."))
	}
	return b.String(), true
}

// trimZeros is the number n without leading zeros; "" and "000" are "0".
func trimZeros(n string) string {
	if t := strings.TrimLeft(n, "0"); t != "" {
		return t
	}
	return "0"
}

// normalizeProjectName is name as PEP 503 spells it in an index: lower case,
// with every run of dots, dashes and underscores one dash.
func normalizeProjectName(name string) string {
	return strings.ToLower(projectSeparators.ReplaceAllString(name, "-"))
}

var projectSeparators = regexp.MustCompile(`[-_.]+`)
