package scaffold

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/JairJosafath/c4factory/internal/model"
)

// trailerUpdate marks the operator's commit that updates a Standard's files, as
// <name>@<from>..<to>, and trailerTake the one that takes a Standard's version
// of files its updates left out, as <name>@<ref>.
const (
	trailerUpdate = "Standard-Update"
	trailerTake   = "Standard-Take"
)

// trailerChecks marks the operator's commit that changes when CI runs the full
// check, as <from>..<to>, and its initial commit, as the checks it rendered.
const trailerChecks = "Checks"

// RenderLayer renders one layer's files with in, as Build does, without the
// other layers, the devcontainer or the identity file.
func RenderLayer(in Input, layer Layer) (Tree, error) {
	files, err := renderLayer(in, layer)
	if err != nil {
		return nil, err
	}
	for _, p := range files.Paths() {
		if isOperatorPath(p) {
			return nil, &ConflictError{Path: p, Owners: [2]string{layer.Name, operatorOwner}}
		}
	}
	return files, nil
}

// Update is what bringing a repository's copy of a layer from one render to
// another changes.
type Update struct {
	// Files are the paths to write, with the new render's content.
	Files Tree
	// Removes are the paths the old render had and the new one doesn't, which
	// the repository still holds as the old render left them.
	Removes []string
	// Merged are the paths of Files both changed, the repository since the old
	// render and the new render too, in different lines: Files holds the two
	// changes merged. Sorted.
	Merged []string
	// LeftOut are the paths both changed that don't merge: their changes
	// overlap, the file is binary, or one side removed it while the other
	// changed it. Sorted.
	LeftOut []string
}

// Paths returns every path the update writes or removes, sorted.
func (u Update) Paths() []string {
	paths := append(u.Files.Paths(), u.Removes...)
	slices.Sort(paths)
	return paths
}

// Empty reports whether the update changes nothing. Paths left out change
// nothing.
func (u Update) Empty() bool {
	return len(u.Files) == 0 && len(u.Removes) == 0
}

// PlanUpdate plans bringing a repository's copy of a layer from the old render
// to the next one. current holds the repository's copy of every path of either
// render, or of behind, that it has. behind are the paths a past update left
// out, each with the render its repository copy was last brought to: such a
// path is planned from that render instead of old, so it is merged against
// its own base and planned even when old and next hold it alike.
//
// A path the next render leaves as the old one made it, or that the repository
// already holds as the next render makes it, needs nothing. A path the
// repository still holds as the old render made it is written, or removed when
// the next render has no such path. Any other path changed in the repository
// since the old render: it is merged line by line with the next render's
// change (Merge3, base the old render) and written when the two don't overlap,
// and left out otherwise.
func PlanUpdate(old, next, current Tree, behind map[string]Tree) Update {
	var u Update
	paths := append(old.Paths(), next.Paths()...)
	for p := range behind {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range slices.Compact(paths) {
		old := old
		if base, ok := behind[p]; ok {
			old = base
		}
		switch {
		case same(old, next, p):
			// The Standard didn't change it.
		case same(current, next, p):
			// The repository already holds the next render.
		case !same(current, old, p):
			// The repository changed it since the old render.
			f, ok := merge(old, next, current, p)
			switch {
			case !ok:
				u.LeftOut = append(u.LeftOut, p)
			case !same(current, Tree{p: f}, p):
				u.write(p, f)
				u.Merged = append(u.Merged, p)
			}
		case next.has(p):
			u.write(p, next[p])
		default:
			u.Removes = append(u.Removes, p)
		}
	}
	return u
}

func (u *Update) write(p string, f File) {
	if u.Files == nil {
		u.Files = Tree{}
	}
	u.Files[p] = f
}

// merge merges the repository's change of p since the old render with the
// next render's, or reports false when they don't merge: a side has no p, the
// file is binary, or the changes overlap. The executable bit is the one of the
// side that changed it.
func merge(old, next, current Tree, p string) (File, bool) {
	base, inOld := old[p]
	theirs, inNext := next[p]
	ours, inCurrent := current[p]
	if !inOld || !inNext || !inCurrent {
		return File{}, false
	}
	data, hunks, err := Merge3(base.Data, ours.Data, theirs.Data)
	if err != nil || len(hunks) > 0 {
		return File{}, false
	}
	executable := theirs.Executable
	if ours.Executable != base.Executable {
		executable = ours.Executable
	}
	return File{Data: data, Executable: executable}, true
}

// Take is what taking a render's version of the files a layer's updates left
// out changes.
type Take struct {
	// Files are the paths to write, and Removes those to remove.
	Files   Tree
	Removes []string
	// Taken are the paths written or removed, sorted, each with the
	// repository's lines it drops.
	Taken []model.TakenFile
}

// Paths returns every path the take writes or removes, sorted.
func (t Take) Paths() []string {
	paths := make([]string, 0, len(t.Taken))
	for _, f := range t.Taken {
		paths = append(paths, f.Path)
	}
	return paths
}

// Empty reports whether the take changes nothing.
func (t Take) Empty() bool {
	return len(t.Taken) == 0
}

// PlanTake plans taking next's version of the files behind, each with the
// render its repository copy was last brought to. current holds the
// repository's copy of each that it has. A file is merged three-way like an
// update's, base its render, with each overlap resolved to next's lines, so
// the repository's changes elsewhere in it stay; one that isn't text, or too
// large to merge, is replaced whole. A file next doesn't have is removed, and
// one the repository already holds as next makes it needs nothing.
func PlanTake(behind map[string]Tree, next, current Tree) Take {
	var t Take
	paths := make([]string, 0, len(behind))
	for p := range behind {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		theirs, inNext := next[p]
		ours, inCurrent := current[p]
		taken := model.TakenFile{Path: p}
		switch {
		case same(current, next, p):
			continue
		case !inNext:
			t.Removes = append(t.Removes, p)
			taken.Note = "The Standard no longer has this file, so the pull request removes it."
		case !inCurrent:
			t.write(p, theirs)
			taken.Note = "The repository had removed this file; the pull request brings back the Standard's."
		default:
			base := behind[p][p]
			data, hunks, err := MergeTheirs(base.Data, ours.Data, theirs.Data)
			if err != nil {
				t.write(p, theirs)
				taken.Note = fmt.Sprintf("It doesn't merge line by line (%v), so the Standard's file replaces "+
					"the repository's whole.", err)
				break
			}
			executable := theirs.Executable
			if ours.Executable != base.Executable {
				executable = ours.Executable
			}
			t.write(p, File{Data: data, Executable: executable})
			for _, h := range hunks {
				if len(h.Ours) > 0 {
					taken.Dropped = append(taken.Dropped, h.Ours)
				}
			}
		}
		t.Taken = append(t.Taken, taken)
	}
	return t
}

func (t *Take) write(p string, f File) {
	if t.Files == nil {
		t.Files = Tree{}
	}
	t.Files[p] = f
}

func (t Tree) has(p string) bool {
	_, ok := t[p]
	return ok
}

// ChangedPaths returns the paths two renders hold differently, sorted: a path
// one has and the other doesn't, or both have with other content or mode.
func ChangedPaths(a, b Tree) []string {
	var paths []string
	for _, p := range a.Paths() {
		if !same(a, b, p) {
			paths = append(paths, p)
		}
	}
	for _, p := range b.Paths() {
		if !a.has(p) {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	return paths
}

// same reports whether two trees hold p alike: neither has it, or both have it
// with the same content and mode.
func same(a, b Tree, p string) bool {
	fa, inA := a[p]
	fb, inB := b[p]
	return inA == inB && (!inA || fa.Executable == fb.Executable && bytes.Equal(fa.Data, fb.Data))
}

// UpdateMessage is the message of the commit that updates in's Container from
// the Standard name at version from to version to.
func UpdateMessage(in Input, name string, from, to model.StandardVersion, u Update) string {
	from, to = from.Normal(), to.Normal()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", model.StandardUpdateTitle(name, from, to))
	fmt.Fprintf(&b, "Brings the files of %s from %s to %s for Container %s.\n", name, from.Ref, to.Ref, in.Container)
	if from.Checks != to.Checks {
		fmt.Fprintf(&b, "CI runs the full check %s.\n", checksWhen(to.Checks))
	}
	if len(u.Merged) > 0 {
		fmt.Fprintf(&b, "Merged with the repository's changes since %s: %s.\n", from.Ref, joinAnd(u.Merged))
	}
	if len(u.LeftOut) > 0 {
		fmt.Fprintf(&b, "Left out, changed in the repository since %s: %s.\n", from.Ref, joinAnd(u.LeftOut))
	}
	fmt.Fprintf(&b, "\n%s: %s/%s\n", trailerContainer, in.Project, in.Container)
	fmt.Fprintf(&b, "%s: %s@%s..%s\n", trailerUpdate, name, from.Ref, to.Ref)
	if from.Checks != to.Checks {
		fmt.Fprintf(&b, "%s: %s..%s\n", trailerChecks, from.Checks, to.Checks)
	}
	return b.String()
}

// TakeMessage is the message of the commit that takes, for in's Container, the
// Standard name's version at ref of the files its updates left out.
func TakeMessage(in Input, name, ref string, t Take) string {
	var b strings.Builder
	paths := t.Paths()
	fmt.Fprintf(&b, "%s\n\n", model.StandardTakeTitle(name, ref, paths))
	fmt.Fprintf(&b, "Takes the version of %s at %s of %s for Container %s, as a person answered: merged with "+
		"the repository's changes, the Standard's lines kept where both changed the same ones.\n",
		name, ref, joinAnd(paths), in.Container)
	fmt.Fprintf(&b, "\n%s: %s/%s\n", trailerContainer, in.Project, in.Container)
	fmt.Fprintf(&b, "%s: %s@%s\n", trailerTake, name, ref)
	return b.String()
}

// checksWhen says when CI runs the full check with checks.
func checksWhen(checks string) string {
	if checks == model.ChecksBeforeRelease {
		return "only before a release"
	}
	return "on every change"
}

// joinAnd joins items the way a sentence lists them: "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// trailerOperations marks the operator's commit that renders infra/operations.ts
// again from a changed spec.operations, as the block it renders.
const trailerOperations = "Operations-Update"

// OperationsMessage is the message of the commit that renders in's
// infra/operations.ts again from in.Operations, a block changed from from.
func OperationsMessage(in Input, from model.Operations) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", model.OperationsUpdateTitle())
	fmt.Fprintf(&b, "The factory rendered %s again from the spec.operations of Container %s; no other file "+
		"changes.\nFrom: %s.\nTo: %s.\n", model.OperationsFile, in.Container, model.OperationsSummary(from),
		model.OperationsSummary(in.Operations))
	fmt.Fprintf(&b, "\n%s: %s/%s\n", trailerContainer, in.Project, in.Container)
	fmt.Fprintf(&b, "%s: %s\n", trailerOperations, strings.TrimPrefix(model.OperationsUpdateBranch(in.Operations),
		"operations-update/"))
	return b.String()
}
