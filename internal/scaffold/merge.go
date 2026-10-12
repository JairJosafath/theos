package scaffold

import (
	"bytes"
	"errors"
	"slices"
)

// ErrBinary is Merge3's refusal of a file that isn't text: one of the three
// holds a NUL byte in its first 8000 bytes, as Git decides it.
var ErrBinary = errors.New("binary file")

// ErrTooLarge is Merge3's refusal of a change too large to diff line by line.
var ErrTooLarge = errors.New("too many changed lines to merge")

// binaryPeek is how much of a file Merge3 looks at for a NUL byte, as Git does.
const binaryPeek = 8000

// maxDiffCells bounds the line diff's table: the changed lines of base times
// those of a side, once the lines both share at the ends are set aside.
const maxDiffCells = 4 << 20

// Hunk is a stretch of base both sides changed, differently: base's lines
// [Start, End), zero-based, became Ours on one side and Theirs on the other.
// Lines keep their line endings.
type Hunk struct {
	Start, End   int
	Ours, Theirs []string
}

// Merge3 merges two changes of base line by line, the way git merge-file
// does: a stretch only one side changed takes that side's lines, one both
// changed alike takes them once, and one both changed differently, or where
// their changes touch, is a Hunk. It returns the merged content when there is
// no Hunk, and the Hunks otherwise. Each line keeps its own ending, so CRLF
// lines and a missing final newline come through as the side that wrote them
// left them. A binary file is refused with ErrBinary.
func Merge3(base, ours, theirs []byte) ([]byte, []Hunk, error) {
	for _, data := range [][]byte{base, ours, theirs} {
		if bytes.IndexByte(data[:min(len(data), binaryPeek)], 0) >= 0 {
			return nil, nil, ErrBinary
		}
	}
	b, o, t := splitLines(base), splitLines(ours), splitLines(theirs)
	oursEdits, err := diffLines(b, o)
	if err != nil {
		return nil, nil, err
	}
	theirsEdits, err := diffLines(b, t)
	if err != nil {
		return nil, nil, err
	}

	var merged []string
	var hunks []Hunk
	pos := 0 // the base lines before pos are merged
	i, j := 0, 0
	// nextEdit is the edit left of either side that starts first, ours on a tie,
	// and its side: 0 for ours, 1 for theirs.
	nextEdit := func() (edit, int, bool) {
		switch {
		case i < len(oursEdits) && (j == len(theirsEdits) || oursEdits[i].start <= theirsEdits[j].start):
			return oursEdits[i], 0, true
		case j < len(theirsEdits):
			return theirsEdits[j], 1, true
		}
		return edit{}, 0, false
	}
	for i < len(oursEdits) || j < len(theirsEdits) {
		// The next group: the first edit left and every edit of either side
		// that overlaps or touches the stretch so far.
		var group [2][]edit
		lo, hi := -1, -1
		for {
			e, side, ok := nextEdit()
			if !ok || lo >= 0 && e.start > hi {
				break
			}
			if lo < 0 {
				lo, hi = e.start, e.end
			}
			hi = max(hi, e.end)
			group[side] = append(group[side], e)
			if side == 0 {
				i++
			} else {
				j++
			}
		}

		merged = append(merged, b[pos:lo]...)
		oursLines := sideLines(b, o, group[0], lo, hi)
		theirsLines := sideLines(b, t, group[1], lo, hi)
		switch {
		case len(group[1]) == 0:
			merged = append(merged, oursLines...)
		case len(group[0]) == 0, slices.Equal(oursLines, theirsLines):
			merged = append(merged, theirsLines...)
		default:
			hunks = append(hunks, Hunk{Start: lo, End: hi, Ours: oursLines, Theirs: theirsLines})
		}
		pos = hi
	}
	if len(hunks) > 0 {
		return nil, hunks, nil
	}
	merged = append(merged, b[pos:]...)
	var out bytes.Buffer
	for _, line := range merged {
		out.WriteString(line)
	}
	return out.Bytes(), nil, nil
}

// edit is one change of a diff: base's lines [start, end) became the side's
// lines [sideStart, sideEnd). Edits of one diff never touch: a line both hold
// lies between any two.
type edit struct {
	start, end, sideStart, sideEnd int
}

// sideLines is what a side made of base's lines [lo, hi), given its edits in
// that stretch: outside them the side holds base's lines unchanged.
func sideLines(base, side []string, edits []edit, lo, hi int) []string {
	if len(edits) == 0 {
		return base[lo:hi]
	}
	first, last := edits[0], edits[len(edits)-1]
	return side[first.sideStart-(first.start-lo) : last.sideEnd+(hi-last.end)]
}

// splitLines splits data after each newline; the last line lacks one when
// data doesn't end with a newline.
func splitLines(data []byte) []string {
	var lines []string
	for len(data) > 0 {
		n := bytes.IndexByte(data, '\n') + 1
		if n == 0 {
			n = len(data)
		}
		lines = append(lines, string(data[:n]))
		data = data[n:]
	}
	return lines
}

// diffLines returns the edits that make side of base, from a longest common
// subsequence of their lines.
func diffLines(base, side []string) ([]edit, error) {
	// The lines both share at the start and the end are no edit.
	pre := 0
	for pre < len(base) && pre < len(side) && base[pre] == side[pre] {
		pre++
	}
	suf := 0
	for suf < len(base)-pre && suf < len(side)-pre && base[len(base)-1-suf] == side[len(side)-1-suf] {
		suf++
	}
	b, s := base[pre:len(base)-suf], side[pre:len(side)-suf]
	if (len(b)+1)*(len(s)+1) > maxDiffCells {
		return nil, ErrTooLarge
	}

	// lcs[x][y] is the length of the longest common subsequence of b[x:] and s[y:].
	width := len(s) + 1
	lcs := make([]int32, (len(b)+1)*width)
	for x, bl := range slices.Backward(b) {
		for y, sl := range slices.Backward(s) {
			if bl == sl {
				lcs[x*width+y] = lcs[(x+1)*width+y+1] + 1
			} else {
				lcs[x*width+y] = max(lcs[(x+1)*width+y], lcs[x*width+y+1])
			}
		}
	}

	var edits []edit
	x, y := 0, 0
	open := func() {
		if len(edits) == 0 || edits[len(edits)-1].end != pre+x || edits[len(edits)-1].sideEnd != pre+y {
			edits = append(edits, edit{start: pre + x, end: pre + x, sideStart: pre + y, sideEnd: pre + y})
		}
	}
	for x < len(b) || y < len(s) {
		switch {
		case x < len(b) && y < len(s) && b[x] == s[y]:
			x++
			y++
		case y == len(s) || x < len(b) && lcs[(x+1)*width+y] >= lcs[x*width+y+1]:
			open()
			x++
			edits[len(edits)-1].end++
		default:
			open()
			y++
			edits[len(edits)-1].sideEnd++
		}
	}
	return edits, nil
}
