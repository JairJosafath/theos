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
	// LeftOut are the paths both changed: the repository changed them since the
	// old render, and the new render changes them too. Sorted.
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
// It goes by whole files. A path the next render leaves as the old one made it,
// or that the repository already holds as the next render makes it, needs
// nothing. A path the repository still holds as the old render made it is
// written, or removed when the next render has no such path. Any other path
// changed in the repository since the old render and is left out.
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
			u.LeftOut = append(u.LeftOut, p)
		case next.has(p):
			if u.Files == nil {
				u.Files = Tree{}
			}
			u.Files[p] = next[p]
		default:
			u.Removes = append(u.Removes, p)
		}
	}
	return u
}

func (t Tree) has(p string) bool {
	_, ok := t[p]
	return ok
}

// same reports whether two trees hold p alike: neither has it, or both have it
// with the same content and mode.
func same(a, b Tree, p string) bool {
	fa, inA := a[p]
	fb, inB := b[p]
	return inA == inB && (!inA || fa.Executable == fb.Executable && bytes.Equal(fa.Data, fb.Data))
}

// UpdateMessage is the message of the commit that updates in's Container from
// the Standard name at ref from to ref to.
func UpdateMessage(in Input, name, from, to string, u Update) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", model.StandardUpdateTitle(name, to))
	fmt.Fprintf(&b, "Brings the files of %s from %s to %s for Container %s.\n", name, from, to, in.Container)
	if len(u.LeftOut) > 0 {
		fmt.Fprintf(&b, "Left out, changed in the repository since %s: %s.\n", from, joinAnd(u.LeftOut))
	}
	fmt.Fprintf(&b, "\n%s: %s/%s\n", trailerContainer, in.Project, in.Container)
	fmt.Fprintf(&b, "%s: %s@%s..%s\n", trailerUpdate, name, from, to)
	return b.String()
}

// joinAnd joins items the way a sentence lists them: "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
