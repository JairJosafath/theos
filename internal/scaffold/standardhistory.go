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

package scaffold

import (
	"bufio"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// StandardAt is the version the files of the Standard name are at, read
// from a repository's history as a cluster keeps it in status (#295):
// messages are the commit messages on main, oldest first. The scaffold's
// commit gives the ref its Scaffolded-From trailer pins and its checks;
// each later commit that names an update of name (Standard-Update
// <name>@<from>..<to>, with Checks <from>..<to> when they change) or an
// adoption of it (Standard-Adoption <name>@<ref>) moves it on. A take
// moves nothing. A trailer is read on any line of a message, a squash
// list's "* " left off, so a squash merge that keeps the commit's message
// or the pull request's description reads back as the commit does. found
// is false when no commit names name: the Standard was not part of the
// scaffold.
func StandardAt(messages []string, name string) (v model.StandardVersion, found bool) {
	for _, msg := range messages {
		if info, err := ParseCommitMessage(msg); err == nil && len(info.ScaffoldedFrom) > 0 {
			if ref := model.ScaffoldedRef(info.ScaffoldedFrom, name); ref != "" {
				v, found = model.StandardVersion{Ref: ref, Checks: info.Checks}.Normal(), true
			}
			continue
		}
		var update, checks string
		scanner := bufio.NewScanner(strings.NewReader(msg))
		for scanner.Scan() {
			line := strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "* ")
			key, value, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			switch key {
			case trailerUpdate:
				if entry, refs, ok := strings.Cut(value, "@"); ok && entry == name {
					if _, to, ok := strings.Cut(refs, ".."); ok && to != "" {
						update = to
					}
				}
			case trailerAdoption:
				if entry, ref, ok := strings.Cut(value, "@"); ok && entry == name && ref != "" {
					update = ref
				}
			case trailerChecks:
				if _, to, ok := strings.Cut(value, ".."); ok {
					checks = to
				}
			}
		}
		if update == "" {
			continue
		}
		v.Ref, found = update, true
		if checks != "" {
			v.Checks = checks
		}
		v = v.Normal()
	}
	return v, found
}
