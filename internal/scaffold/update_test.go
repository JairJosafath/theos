package scaffold

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/JairJosafath/c4factory/internal/model"
)

func TestPlanUpdate(t *testing.T) {
	script := func(data string, executable bool) Tree {
		return Tree{"run.sh": File{Data: []byte(data), Executable: executable}}
	}
	tests := []struct {
		name               string
		old, next, current Tree
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanUpdate(tt.old, tt.next, tt.current); !reflect.DeepEqual(got, tt.want) {
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

// checksStandard is a Standard whose CI workflow follows the Container's checks.
var checksStandard = tree(
	".gitea/workflows/check.yaml.tmpl", "jobs:\n  check:\n"+
		"    run: make {{ if eq .Checks \"BeforeRelease\" }}check-light{{ else }}check{{ end }}\n"+
		"{{- if eq .Checks \"BeforeRelease\" }}\n  full:\n    on: release-check/**\n    run: make check\n{{- end }}\n",
	"AGENTS.md.tmpl", "{{ .Container }} follows the Standard.\n")

func TestPlanUpdateBetweenChecksModes(t *testing.T) {
	render := func(checks string) Tree {
		in := ordersAPI
		in.Standard, in.Checks = "go-standard", checks
		files, err := RenderLayer(in, Layer{Side: model.SideStandard, Name: "go-standard", Ref: "v1.0.0",
			Files: checksStandard})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	const workflow = ".gitea/workflows/check.yaml"
	every, before := render(model.ChecksEveryChange), render(model.ChecksBeforeRelease)
	if !slices.Equal(ChangedPaths(every, before), []string{workflow}) {
		t.Fatalf("ChangedPaths() = %v, want only %s", ChangedPaths(every, before), workflow)
	}
	if got := render(""); !reflect.DeepEqual(got, every) {
		t.Errorf("empty checks render %q, want EveryChange's %q", got[workflow].Data, every[workflow].Data)
	}

	tests := []struct {
		name               string
		old, next, current Tree
		want               Update
	}{
		{name: "to BeforeRelease", old: every, next: before, current: every,
			want: Update{Files: Tree{workflow: before[workflow]}}},
		{name: "back to EveryChange", old: before, next: every, current: before,
			want: Update{Files: Tree{workflow: every[workflow]}}},
		{name: "a hand-changed workflow is left out", old: every, next: before,
			current: Tree{workflow: File{Data: []byte("jobs: {}\n")}, "AGENTS.md": every["AGENTS.md"]},
			want:    Update{LeftOut: []string{workflow}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanUpdate(tt.old, tt.next, tt.current)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PlanUpdate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestChangedPaths(t *testing.T) {
	a := tree("same", "x", "changed", "a", "removed", "r")
	b := tree("same", "x", "changed", "b", "added", "n")
	b["mode"], a["mode"] = File{Data: []byte("m"), Executable: true}, File{Data: []byte("m")}
	if got, want := ChangedPaths(a, b), []string{"added", "changed", "mode", "removed"}; !slices.Equal(got, want) {
		t.Errorf("ChangedPaths() = %v, want %v", got, want)
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
	if got := UpdateMessage(in, "go-standard", model.StandardVersion{Ref: "v1.0.0"}, model.StandardVersion{Ref: "v1.1.1"}, u); got != want {
		t.Errorf("UpdateMessage() =\n%s\nwant\n%s", got, want)
	}

	u.LeftOut = []string{"Makefile", "docs/style/go.md"}
	want = "Update the Standard go-standard to v1.1.1\n\n" +
		"Brings the files of go-standard from v1.0.0 to v1.1.1 for Container cel-language-server.\n" +
		"Left out, changed in the repository since v1.0.0: Makefile and docs/style/go.md.\n\n" +
		"C4-Factory-Container: tools/cel-language-server\n" +
		"Standard-Update: go-standard@v1.0.0..v1.1.1\n"
	if got := UpdateMessage(in, "go-standard", model.StandardVersion{Ref: "v1.0.0"}, model.StandardVersion{Ref: "v1.1.1"}, u); got != want {
		t.Errorf("UpdateMessage() with paths left out =\n%s\nwant\n%s", got, want)
	}

	u.Merged = []string{".gitea/workflows/check.yaml"}
	want = "Update the Standard go-standard to v1.1.1\n\n" +
		"Brings the files of go-standard from v1.0.0 to v1.1.1 for Container cel-language-server.\n" +
		"Merged with the repository's changes since v1.0.0: .gitea/workflows/check.yaml.\n" +
		"Left out, changed in the repository since v1.0.0: Makefile and docs/style/go.md.\n\n" +
		"C4-Factory-Container: tools/cel-language-server\n" +
		"Standard-Update: go-standard@v1.0.0..v1.1.1\n"
	if got := UpdateMessage(in, "go-standard", model.StandardVersion{Ref: "v1.0.0"}, model.StandardVersion{Ref: "v1.1.1"}, u); got != want {
		t.Errorf("UpdateMessage() with paths merged =\n%s\nwant\n%s", got, want)
	}
}

func TestUpdateMessageBetweenChecksModes(t *testing.T) {
	in := Input{Project: "tools", SoftwareSystem: "tools", Container: "cel-language-server"}
	u := Update{Files: tree(".gitea/workflows/check.yaml", "x")}
	want := "Update the Standard go-standard to v1.1.1, with the full check before release\n\n" +
		"Brings the files of go-standard from v1.0.0 to v1.1.1 for Container cel-language-server.\n" +
		"CI runs the full check only before a release.\n\n" +
		"C4-Factory-Container: tools/cel-language-server\n" +
		"Standard-Update: go-standard@v1.0.0..v1.1.1\n" +
		"Checks: EveryChange..BeforeRelease\n"
	got := UpdateMessage(in, "go-standard", model.StandardVersion{Ref: "v1.0.0"},
		model.StandardVersion{Ref: "v1.1.1", Checks: model.ChecksBeforeRelease}, u)
	if got != want {
		t.Errorf("UpdateMessage() =\n%s\nwant\n%s", got, want)
	}

	want = "Update the Standard go-standard to v1.1.1, with the full check on every change\n\n" +
		"Brings the files of go-standard from v1.1.1 to v1.1.1 for Container cel-language-server.\n" +
		"CI runs the full check on every change.\n\n" +
		"C4-Factory-Container: tools/cel-language-server\n" +
		"Standard-Update: go-standard@v1.1.1..v1.1.1\n" +
		"Checks: BeforeRelease..EveryChange\n"
	got = UpdateMessage(in, "go-standard", model.StandardVersion{Ref: "v1.1.1", Checks: model.ChecksBeforeRelease},
		model.StandardVersion{Ref: "v1.1.1"}, u)
	if got != want {
		t.Errorf("UpdateMessage() back to EveryChange =\n%s\nwant\n%s", got, want)
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
