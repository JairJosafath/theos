package scaffold

import (
	"slices"
	"strings"
	"testing"
)

// ts-standard's .prettierignore lists docs/agents/, the folder the factory
// writes and agents never edit: the Standard's own guides and every Domain's
// Markdown. Prettier checking it failed make check on a fresh scaffold with a
// Domain, at fmt-check, before anyone changed a line (#38).
func TestTSStandardPrettierSkipsDocsAgents(t *testing.T) {
	typ, sides := standardCombo(t, "ts-standard")
	in, layers := loadCombo(t, typ, sides, "")
	out, err := Build(in, layers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ignore, ok := out.Files[".prettierignore"]
	if !ok {
		t.Fatal("ts-standard renders no .prettierignore")
	}
	if !slices.Contains(strings.Split(string(ignore.Data), "\n"), "docs/agents/") {
		t.Errorf(".prettierignore does not list docs/agents/:\n%s", ignore.Data)
	}
}
