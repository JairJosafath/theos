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
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

// guidedStandards are the Standards that render an agent guide and a
// reviewer guide; the hello Standards are the end-to-end tests' stand-ins.
var guidedStandards = []string{"go-standard", "go-operator-standard", "ts-standard", "python-standard", "aws-standard"}

// Each Standard's AGENTS.md says which Domains the system is about and to
// read their guides before planning a change, and its reviewer guide says to
// check the change against each domain's common mistakes, only when the
// Container's system names Domains (docs/plans/domains.md, decision 4);
// without them both render as before, to the byte (#1758).
func TestStandardsPointAtTheDomains(t *testing.T) {
	const section = "## The domains\n"
	guides := map[string][]string{
		"AGENTS.md": {
			"This system is about billing, webshop.",
			"- `docs/agents/domain/billing/README.md`\n- `docs/agents/domain/webshop/README.md`\n",
			"make theirs unless the\nissue says otherwise",
		},
		"docs/agents/reviewer.md": {
			"This system is about billing, webshop.",
			"each domain's *Common mistakes*",
			"- `docs/agents/domain/billing/README.md`\n- `docs/agents/domain/webshop/README.md`\n",
			"A change that makes one is blocking",
		},
	}
	for _, standard := range guidedStandards {
		t.Run(standard, func(t *testing.T) {
			typ, sides := standardCombo(t, standard)
			// The combo's own Domain (the webshop's, #1759) would be a third
			// one; the guides are rendered without, then with the two below.
			sides = slices.DeleteFunc(slices.Clone(sides), func(s side) bool { return s.Side == model.SideDomain })
			in, layers := loadCombo(t, typ, sides, "")
			without, err := Build(in, layers, Options{})
			if err != nil {
				t.Fatal(err)
			}
			in.Domains = []string{"billing", "webshop"}
			layers = append(layers,
				Layer{Side: model.SideDomain, Name: "billing", Ref: "v0.2.0", Files: tree("README.md", "# Billing\n")},
				Layer{Side: model.SideDomain, Name: "webshop", Ref: "v0.1.0", Files: tree("README.md", "# Webshop\n", "carts.md", "# Carts\n")},
			)
			with, err := Build(in, layers, Options{})
			if err != nil {
				t.Fatal(err)
			}
			for path, wants := range guides {
				if ownerOf(layers, path) != standard {
					t.Fatalf("%s is written by %q, want the Standard", path, ownerOf(layers, path))
				}
				got := string(with.Files[path].Data)
				assertContains(t, path+" with Domains", got, append([]string{section}, wants...))
				plain := string(without.Files[path].Data)
				for _, never := range []string{section, "docs/agents/domain/"} {
					if strings.Contains(plain, never) {
						t.Errorf("%s without Domains mentions %q", path, never)
					}
				}
				// The section is the only difference: cut it, from its heading
				// to the next one, and the guide is the one without Domains.
				start := strings.Index(got, section)
				if start < 0 {
					continue
				}
				next := strings.Index(got[start+len(section):], "\n## ")
				if next < 0 {
					t.Fatalf("%s: no section follows %q", path, section)
				}
				if cut := got[:start] + got[start+len(section)+next+1:]; cut != plain {
					t.Errorf("%s with Domains differs from without beyond the domains section:\n%s", path, cut)
				}
			}
		})
	}
}
