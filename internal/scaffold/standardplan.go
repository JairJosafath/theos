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
	"cmp"
	"fmt"
	"slices"

	"github.com/JairJosafath/theos/internal/model"
)

// A Standard's update, as the operator's Container reconciler and theos
// update both plan it (#295): the Standard rendered at the version the
// repository's files are at and at the one they go to, then PlanUpdate
// against main. One function for both, so the two can't drift.

// StandardRenderError is a Standard that fetched but did not render at a
// version.
type StandardRenderError struct {
	Version model.StandardVersion
	Err     error
}

func (e *StandardRenderError) Error() string {
	return fmt.Sprintf("at %s: %v", e.Version, e.Err)
}

func (e *StandardRenderError) Unwrap() error { return e.Err }

// RenderStandard renders the Standard name's files, its template's files at
// v.Ref, for in's Container with v's checks and the CI images images.
func RenderStandard(in Input, name string, v model.StandardVersion, images model.CIImages, files Tree) (Tree,
	error) {
	in.Checks, in.CIImages = v.Checks, images
	tree, err := RenderLayer(in, Layer{Side: model.SideStandard, Name: name, Ref: v.Ref, Files: files})
	if err != nil {
		return nil, &StandardRenderError{Version: v, Err: err}
	}
	return tree, nil
}

// StandardRenders are what a Standard's update from one version to another
// is planned from.
type StandardRenders struct {
	Name     string
	From, To model.StandardVersion
	// Old is the Standard at From, rendered with the CI images main's files
	// were rendered with; Next at To, with the images the update brings.
	Old, Next Tree
	// ChecksPaths are the files the change of checks writes, when From and
	// To differ in their checks: To's ref rendered with From's checks
	// against Next.
	ChecksPaths []string
}

// RenderStandardUpdate renders the Standard name for in's Container at from,
// from its template's files oldFiles and with fromImages, the CI images
// main's files were rendered with (#284), and at to, from nextFiles and with
// in.CIImages, the ones the update brings.
func RenderStandardUpdate(in Input, name string, from, to model.StandardVersion, fromImages model.CIImages,
	oldFiles, nextFiles Tree) (StandardRenders, error) {
	r := StandardRenders{Name: name, From: from, To: to}
	var err error
	if r.Old, err = RenderStandard(in, name, from, fromImages, oldFiles); err != nil {
		return StandardRenders{}, err
	}
	if r.Next, err = RenderStandard(in, name, to, in.CIImages, nextFiles); err != nil {
		return StandardRenders{}, err
	}
	if from.Checks != to.Checks {
		keep, err := RenderStandard(in, name, model.StandardVersion{Ref: to.Ref, Checks: from.Checks}, in.CIImages,
			nextFiles)
		if err != nil {
			return StandardRenders{}, err
		}
		r.ChecksPaths = ChangedPaths(keep, r.Next)
	}
	return r, nil
}

// Paths are the paths the plan reads from main: every path of Old and Next,
// and of behind, sorted.
func (r StandardRenders) Paths(behind map[string]Tree) []string {
	paths := append(r.Old.Paths(), r.Next.Paths()...)
	for p := range behind {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// StandardPlan is a Standard's update planned against main.
type StandardPlan struct {
	Update
	// ChecksLeftOut are the files the change of checks writes that the
	// update leaves out: CI can't change its checks, and nothing is opened.
	ChecksLeftOut []string
	// NoLightCheck says the update changes CI to run the full check only
	// before a release and leaves a Makefile with no light check: it is held
	// until the Makefile has one.
	NoLightCheck bool
}

// Plan plans the update against current, main's copy of r.Paths(behind);
// behind are, by path, the renders the files a past update left out were
// last brought to, as PlanUpdate takes them.
func (r StandardRenders) Plan(current Tree, behind map[string]Tree) StandardPlan {
	p := StandardPlan{Update: PlanUpdate(r.Old, r.Next, current, behind)}
	for _, path := range p.LeftOut {
		if slices.Contains(r.ChecksPaths, path) {
			p.ChecksLeftOut = append(p.ChecksLeftOut, path)
		}
	}
	p.NoLightCheck = len(p.ChecksLeftOut) == 0 && r.To.Checks == model.ChecksBeforeRelease &&
		r.From.Checks != r.To.Checks && !model.HasLightCheck(p.FileAfter(current, model.LightCheckFile))
	return p
}

// FileAfter is path's content once u merges into current, or nil when it
// has no such path.
func (u Update) FileAfter(current Tree, path string) []byte {
	if f, ok := u.Files[path]; ok {
		return f.Data
	}
	if slices.Contains(u.Removes, path) {
		return nil
	}
	return current[path].Data
}

// LeftOut is what the update's pull request says of the files it leaves
// out, paths: each one's change between the Standard's render at the ref
// its copy is at (Old at From, or its render in behind, at its ref in refs)
// and Next, and what taking Next's version of it would drop from current.
func (r StandardRenders) LeftOut(behind map[string]Tree, refs map[string]string, current Tree,
	paths []string) []model.LeftOutFile {
	files := map[string]Tree{}
	for _, p := range paths {
		files[p] = r.Old
		if base, ok := behind[p]; ok {
			files[p] = base
		}
	}
	take := PlanTake(files, r.Next, current)
	out := make([]model.LeftOutFile, 0, len(paths))
	for _, p := range paths {
		change := StandardChange(files[p], r.Next, p)
		f := model.LeftOutFile{Path: p, Ref: cmp.Or(refs[p], r.From.Ref), Diff: change.Diff, Note: change.Note}
		if i := slices.IndexFunc(take.Taken, func(t model.TakenFile) bool { return t.Path == p }); i >= 0 {
			f.Take = take.Taken[i]
		}
		out = append(out, f)
	}
	return out
}
