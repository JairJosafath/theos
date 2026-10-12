package scaffold

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/JairJosafath/c4factory/internal/model"
)

// trailerUpdate marks the operator's commit that updates a Standard's files, as
// <name>@<from>..<to>.
const trailerUpdate = "Standard-Update"

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
// render that it has.
//
// A path the next render leaves as the old one made it, or that the repository
// already holds as the next render makes it, needs nothing. A path the
// repository still holds as the old render made it is written, or removed when
// the next render has no such path. Any other path changed in the repository
// since the old render: it is merged line by line with the next render's
// change (Merge3, base the old render) and written when the two don't overlap,
// and left out otherwise.
func PlanUpdate(old, next, current Tree) Update {
	var u Update
	paths := append(old.Paths(), next.Paths()...)
	slices.Sort(paths)
	for _, p := range slices.Compact(paths) {
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
