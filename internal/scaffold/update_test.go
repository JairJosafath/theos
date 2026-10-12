package scaffold

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestPlanUpdate(t *testing.T) {
	script := func(data string, executable bool) Tree {
		return Tree{"run.sh": File{Data: []byte(data), Executable: executable}}
	}
	tests := []struct {
		name               string
		old, next, current Tree
		behind             map[string]Tree
		want               Update
	}{
		{name: "unchanged by the Standard",
			old: tree("Makefile", "a"), next: tree("Makefile", "a"), current: tree("Makefile", "changed here")},
		{name: "already at next",
			old: tree("Makefile", "a"), next: tree("Makefile", "b"), current: tree("Makefile", "b")},
		{name: "clean write",
			old: tree("Makefile", "a"), next: tree("Makefile", "b"), current: tree("Makefile", "a"),
			want: Update{Files: tree("Makefile", "b")}},
		{name: "clean remove",
			old: tree("Makefile", "a"), next: Tree{}, current: tree("Makefile", "a"),
			want: Update{Removes: []string{"Makefile"}}},
		{name: "removed already",
			old: tree("Makefile", "a"), next: Tree{}, current: Tree{}},
		{name: "new path absent from the repository",
			old: Tree{}, next: tree("docs/agents/implementer.md", "b"), current: Tree{},
			want: Update{Files: tree("docs/agents/implementer.md", "b")}},
		{name: "new path the repository has with other content",
			old: Tree{}, next: tree("Dockerfile", "b"), current: tree("Dockerfile", "from another layer"),
			want: Update{LeftOut: []string{"Dockerfile"}}},
		{name: "changed path the repository changed too",
			old: tree("Makefile", "a"), next: tree("Makefile", "b"), current: tree("Makefile", "changed here"),
			want: Update{LeftOut: []string{"Makefile"}}},
		{name: "removed path the repository changed",
			old: tree("Makefile", "a\nb\n"), next: Tree{}, current: tree("Makefile", "a\nB\n"),
			want: Update{LeftOut: []string{"Makefile"}}},
		{name: "changed path the repository deleted",
			old: tree("Makefile", "a\nb\n"), next: tree("Makefile", "a\nB\n"), current: Tree{},
			want: Update{LeftOut: []string{"Makefile"}}},
		{name: "the executable bit changes",
			old: script("x", false), next: script("x", true), current: script("x", false),
			want: Update{Files: script("x", true)}},
		{name: "the repository changed only the executable bit",
			old: script("x", false), next: script("y", false), current: script("x", true),
			want: Update{Files: script("y", true), Merged: []string{"run.sh"}}},
		{name: "a clean merge written",
			old:     tree("Makefile", "check:\n\ttrue\n\nbuild:\n\tgo build\n"),
			next:    tree("Makefile", "check:\n\tmake lint test\n\nbuild:\n\tgo build\n"),
			current: tree("Makefile", "check:\n\ttrue\n\nbuild:\n\tgo build ./...\n"),
			want: Update{
				Files:  tree("Makefile", "check:\n\tmake lint test\n\nbuild:\n\tgo build ./...\n"),
				Merged: []string{"Makefile"},
			}},
		{name: "a merge the repository already holds",
			old:     tree("Makefile", "a\nb\nc\n"),
			next:    tree("Makefile", "A\nb\nc\n"),
			current: tree("Makefile", "A\nb\nC\n")},
		{name: "overlapping changes left out",
			old:     tree("Makefile", "a\nb\nc\n"),
			next:    tree("Makefile", "a\nB\nc\n"),
			current: tree("Makefile", "a\nours\nc\n"),
			want:    Update{LeftOut: []string{"Makefile"}}},
		{name: "a binary file left out",
			old:     tree("logo.png", "a\x00\nb\nc\n"),
			next:    tree("logo.png", "A\x00\nb\nc\n"),
			current: tree("logo.png", "a\x00\nb\nC\n"),
			want:    Update{LeftOut: []string{"logo.png"}}},
		{name: "many paths, sorted",
			old:     tree("a", "1", "b", "1", "c", "1", "d", "1", "e", "1", "g", "x\ny\n"),
			next:    tree("a", "2", "c", "2", "e", "1", "f", "2", "g", "x\ny\nz\n"),
			current: tree("a", "1", "b", "1", "c", "changed here", "d", "1", "e", "changed here", "g", "X\ny\n"),
			want: Update{
				Files:   tree("a", "2", "f", "2", "g", "X\ny\nz\n"),
				Removes: []string{"b", "d"},
				Merged:  []string{"g"},
				LeftOut: []string{"c"},
			}},
		{name: "behind, unchanged by this step, still overlapping",
			old:     tree("Makefile", "a\nB\nc\n"),
			next:    tree("Makefile", "a\nB\nc\n"),
			current: tree("Makefile", "a\nours\nc\n"),
			behind:  map[string]Tree{"Makefile": tree("Makefile", "a\nb\nc\n")},
			want:    Update{LeftOut: []string{"Makefile"}}},
		{name: "behind, merged against its own base",
			old:     tree("Makefile", "a\nB\nc\nd\n"),
			next:    tree("Makefile", "a\nB\nc\nd\n"),
			current: tree("Makefile", "a\nb\nc\nours\n"),
			behind:  map[string]Tree{"Makefile": tree("Makefile", "a\nb\nc\nd\n")},
			want: Update{
				Files:  tree("Makefile", "a\nB\nc\nours\n"),
				Merged: []string{"Makefile"},
			}},
		{name: "behind, brought to next in the repository",
			old:     tree("Makefile", "a\nB\nc\n"),
			next:    tree("Makefile", "a\nB2\nc\n"),
			current: tree("Makefile", "a\nB2\nc\n"),
			behind:  map[string]Tree{"Makefile": tree("Makefile", "a\nb\nc\n")}},
		{name: "behind, still at its base",
			old:     tree("Makefile", "a\nB\nc\n"),
			next:    tree("Makefile", "a\nB\nc\n"),
			current: tree("Makefile", "a\nb\nc\n"),
			behind:  map[string]Tree{"Makefile": tree("Makefile", "a\nb\nc\n")},
			want:    Update{Files: tree("Makefile", "a\nB\nc\n")}},
		{name: "behind, a path the next render no longer has",
			old:     tree("Makefile", "a\nB\nc\n"),
			next:    Tree{},
			current: tree("Makefile", "a\nours\nc\n"),
			behind:  map[string]Tree{"Makefile": tree("Makefile", "a\nb\nc\n")},
			want:    Update{LeftOut: []string{"Makefile"}}},
		{name: "behind, a path neither render has",
			old:     Tree{},
			next:    Tree{},
			current: tree("Makefile", "a\nours\nc\n"),
			behind:  map[string]Tree{"Makefile": tree("Makefile", "a\nb\nc\n")},
			want:    Update{LeftOut: []string{"Makefile"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanUpdate(tt.old, tt.next, tt.current, tt.behind); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PlanUpdate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestUpdatePathsAndEmpty(t *testing.T) {
	u := Update{Files: tree("d", "x", "b", "x"), Removes: []string{"a", "c"}, LeftOut: []string{"e"}}
	if got, want := u.Paths(), []string{"a", "b", "c", "d"}; !slices.Equal(got, want) {
		t.Errorf("Paths() = %v, want %v", got, want)
	}
	for _, tt := range []struct {
		name string
		u    Update
		want bool
	}{
		{name: "nothing", u: Update{}, want: true},
		{name: "only paths left out", u: Update{LeftOut: []string{"Makefile"}}, want: true},
		{name: "a write", u: Update{Files: tree("Makefile", "b")}},
		{name: "a remove", u: Update{Removes: []string{"Makefile"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.u.Empty(); got != tt.want {
				t.Errorf("Empty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderLayerRendersLikeBuild(t *testing.T) {
	layers := webshopLayers()
	got, err := RenderLayer(ordersAPI, layers[0])
	if err != nil {
		t.Fatalf("RenderLayer() error = %v", err)
	}
	// The template's .c4factory/ input is never copied.
	if want := []string{"app/main.py", "pyproject.toml"}; !slices.Equal(got.Paths(), want) {
		t.Fatalf("paths = %v, want %v", got.Paths(), want)
	}
	built, err := Build(ordersAPI, layers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Paths() {
		if !reflect.DeepEqual(got[p], built.Files[p]) {
			t.Errorf("%s = %q, Build wrote %q", p, got[p].Data, built.Files[p].Data)
		}
	}
}

func TestRenderLayerRefusesOperatorPaths(t *testing.T) {
	for _, tt := range []struct{ path, want string }{
		{".devcontainer/devcontainer.json", "python-standard and the operator both write .devcontainer/devcontainer.json"},
		{".c4factory.yaml", "python-standard and the operator both write .c4factory.yaml"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			layer := withStandard(webshopLayers())[1]
			layer.Files[tt.path] = File{Data: []byte("x")}
			_, err := RenderLayer(ordersAPI, layer)
			if _, ok := errors.AsType[*ConflictError](err); !ok {
				t.Fatalf("RenderLayer() error = %v, want *ConflictError", err)
			}
			if err.Error() != tt.want {
				t.Errorf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestUpdateMessage(t *testing.T) {
	in := Input{Project: "tools", SoftwareSystem: "tools", Container: "cel-language-server"}
	u := Update{Files: tree("AGENTS.md", "x")}
	want := "Update the Standard go-standard to v1.1.1\n\n" +
		"Brings the files of go-standard from v1.0.0 to v1.1.1 for Container cel-language-server.\n\n" +
		"C4-Factory-Container: tools/cel-language-server\n" +
		"Standard-Update: go-standard@v1.0.0..v1.1.1\n"
	if got := UpdateMessage(in, "go-standard", "v1.0.0", "v1.1.1", u); got != want {
		t.Errorf("UpdateMessage() =\n%s\nwant\n%s", got, want)
	}

	u.LeftOut = []string{"Makefile", "docs/style/go.md"}
	want = "Update the Standard go-standard to v1.1.1\n\n" +
		"Brings the files of go-standard from v1.0.0 to v1.1.1 for Container cel-language-server.\n" +
		"Left out, changed in the repository since v1.0.0: Makefile and docs/style/go.md.\n\n" +
		"C4-Factory-Container: tools/cel-language-server\n" +
		"Standard-Update: go-standard@v1.0.0..v1.1.1\n"
	if got := UpdateMessage(in, "go-standard", "v1.0.0", "v1.1.1", u); got != want {
		t.Errorf("UpdateMessage() with paths left out =\n%s\nwant\n%s", got, want)
	}

	u.Merged = []string{".gitea/workflows/check.yaml"}
	want = "Update the Standard go-standard to v1.1.1\n\n" +
		"Brings the files of go-standard from v1.0.0 to v1.1.1 for Container cel-language-server.\n" +
		"Merged with the repository's changes since v1.0.0: .gitea/workflows/check.yaml.\n" +
		"Left out, changed in the repository since v1.0.0: Makefile and docs/style/go.md.\n\n" +
		"C4-Factory-Container: tools/cel-language-server\n" +
		"Standard-Update: go-standard@v1.0.0..v1.1.1\n"
	if got := UpdateMessage(in, "go-standard", "v1.0.0", "v1.1.1", u); got != want {
		t.Errorf("UpdateMessage() with paths merged =\n%s\nwant\n%s", got, want)
	}
}

func TestJoinAnd(t *testing.T) {
	for _, tt := range []struct {
		items []string
		want  string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a and b"},
		{[]string{"a", "b", "c"}, "a, b and c"},
	} {
		if got := joinAnd(tt.items); got != tt.want {
			t.Errorf("joinAnd(%q) = %q, want %q", tt.items, got, tt.want)
		}
	}
}
