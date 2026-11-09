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
	"fmt"
	"strings"
	"time"
)

// A Brief lists each Container it made whose Standard files an update left
// out under Needs you (plans/done/standard-left-out.md, step 3). The person answers
// it on the Container, with one annotation; no autopilot mode takes it.

// BehindFile is one of a Container's Standard files an update left out, with
// the ref its repository copy was last brought to.
type BehindFile struct {
	Path string
	Ref  string
}

// StandardBehind is a Container whose Standard files wait on a person
// (StandardUpdated False, FilesBehind).
type StandardBehind struct {
	Container string
	// Standard is the Standard's name, and Ref the ref the Container is at.
	Standard string
	Ref      string
	Files    []BehindFile
	// Take is the annotation that takes the Standard's version, name=ref.
	Take string
	// Since is when the Container was flagged.
	Since time.Time
}

// StandardBehindNeedsYou is the Needs you entry of a Container whose Standard
// files are behind: the Brief would take the Standard's version.
func StandardBehindNeedsYou(b StandardBehind) NeedsYou {
	files := make([]string, 0, len(b.Files))
	for _, f := range b.Files {
		files = append(files, fmt.Sprintf("%s (at %s)", f.Path, f.Ref))
	}
	return NeedsYou{Kind: AttentionContainer, Name: b.Container,
		Question: fmt.Sprintf("Container `%s` is at %s %s, but its updates left out %s: the repository changed "+
			"them in lines the Standard changed too.", b.Container, b.Standard, b.Ref, strings.Join(files, ", ")),
		Recommend: fmt.Sprintf("Take the Standard's version: annotate the Container `%s`.", b.Take),
		Why: fmt.Sprintf("The Standard's fix (%s %s) reaches the app; the repository's own lines that overlap it "+
			"are listed in the pull request and can be added back. What the Standard changed in each file, "+
			"and the lines a take drops, is on the Container's page.", b.Standard, b.Ref),
		Since: b.Since}
}
