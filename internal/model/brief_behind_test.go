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
	"testing"
	"time"
)

func TestStandardBehindNeedsYou(t *testing.T) {
	b := StandardBehind{Container: "cel-lens-core", Standard: "go-standard", Ref: "v1.2.0",
		Files: []BehindFile{{".gitea/workflows/check.yaml", "v1.1.6"}, {"Makefile", "v1.2.0"}},
		Take:  "theos.factory.io/standard-take=v1.2.0", Since: time.Unix(5, 0)}
	got := StandardBehindNeedsYou(b)
	want := NeedsYou{Kind: AttentionContainer, Name: "cel-lens-core",
		Question: "Container `cel-lens-core` is at go-standard v1.2.0, but its updates left out " +
			".gitea/workflows/check.yaml (at v1.1.6), Makefile (at v1.2.0): the repository changed them in lines " +
			"the Standard changed too.",
		Recommend: "Take the Standard's version: annotate the Container `theos.factory.io/standard-take=v1.2.0`.",
		Why: "The Standard's fix (go-standard v1.2.0) reaches the app; the repository's own lines that overlap " +
			"it are listed in the pull request and can be added back. What the Standard changed in each file, " +
			"and the lines a take drops, is on the Container's page.",
		Since: time.Unix(5, 0)}
	if got != want {
		t.Errorf("StandardBehindNeedsYou =\n%+v\nwant\n%+v", got, want)
	}
}
