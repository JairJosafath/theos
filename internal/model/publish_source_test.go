package model

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

// Blob and tree SHA-1s git prints for fixed contents, from git 2.53:
//
//	printf 'package engine\n' | git hash-object --stdin
//	printf "100644 blob $a\tparse.go\n" | git mktree
const (
	engineGo   = "00a22ef62d0892081db869c2b7218e03c7af8770" // "package engine\n"
	runSh      = "4163036efa65bd4a469e752267498f01ea36a55c" // "#!/bin/sh\necho hi\n"
	linkBlob   = "ad1bc92930b6e5d72cd777131c38a4f4a1973e2b" // "engine.go", a symlink's target
	goModBlob  = "24f6cfc14c620bf756a69294102921ddcb69744a" // "module github.com/JairJosafath/cel-lens/engine\n"
	emptyBlob  = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" // ""
	cmdTree    = "bed74851dc23518a86bf570515304a4008415872" // cmd/: parse.go, engineGo's blob
	sourceTree = "f4d81530252b13745e86f44f8d13a0b1214e23ff" // the files of engineSource
	emptyTree  = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
)

// engineSource is a source with a file, an executable, a symlink, a folder,
// and a file cmd.go that git sorts before the folder cmd.
func engineSource() []SourceFile {
	return []SourceFile{
		{Path: "go.mod", Mode: ModeFile, Size: 47, Blob: goModBlob},
		{Path: "engine.go", Mode: ModeFile, Size: 15, Blob: engineGo},
		{Path: "run.sh", Mode: ModeExecutable, Size: 18, Blob: runSh},
		{Path: "link", Mode: ModeSymlink, Size: 9, Blob: linkBlob},
		{Path: "cmd/parse.go", Mode: ModeFile, Size: 15, Blob: engineGo},
		{Path: "cmd.go", Mode: ModeFile, Blob: emptyBlob},
	}
}

func TestFactoryOwnPaths(t *testing.T) {
	want := []string{".theos.yaml", ".devcontainer/", ".gitea/", "AGENTS.md", "docs/agents/"}
	if got := FactoryOwnPaths(giteaActions); !slices.Equal(got, want) {
		t.Errorf("FactoryOwnPaths(giteaActions) = %q, want %q", got, want)
	}
}

func TestBlobSHA(t *testing.T) {
	for content, want := range map[string]string{
		"package engine\n": engineGo, "#!/bin/sh\necho hi\n": runSh, "engine.go": linkBlob, "": emptyBlob,
		"module github.com/JairJosafath/cel-lens/engine\n": goModBlob,
	} {
		if got := BlobSHA([]byte(content)); got != want {
			t.Errorf("BlobSHA(%q) = %s, want %s", content, got, want)
		}
		got, err := HashBlob(strings.NewReader(content), int64(len(content)))
		if err != nil || got != want {
			t.Errorf("HashBlob(%q) = %s, %v, want %s", content, got, err, want)
		}
	}
	if _, err := HashBlob(strings.NewReader("short"), 10); err == nil {
		t.Error("HashBlob of a short read: no error")
	}
	if _, err := HashBlob(iotest.ErrReader(fmt.Errorf("boom")), 10); err == nil {
		t.Error("HashBlob of a failed read: no error")
	}
}

func TestTreeSHA(t *testing.T) {
	got, err := TreeSHA(engineSource())
	if err != nil || got != sourceTree {
		t.Errorf("TreeSHA(engine) = %s, %v, want %s", got, err, sourceTree)
	}
	if got, _ := TreeSHA([]SourceFile{{Path: "parse.go", Mode: ModeFile, Blob: engineGo}}); got != cmdTree {
		t.Errorf("TreeSHA(parse.go) = %s, want %s", got, cmdTree)
	}
	if got, _ := TreeSHA(nil); got != emptyTree {
		t.Errorf("TreeSHA(nil) = %s, want the empty tree %s", got, emptyTree)
	}
	// The order files come in doesn't matter.
	files := engineSource()
	slices.Reverse(files)
	if got, _ := TreeSHA(files); got != sourceTree {
		t.Errorf("TreeSHA(reversed) = %s, want %s", got, sourceTree)
	}

	bad := map[string][]SourceFile{
		"twice":              {{Path: "a", Mode: ModeFile, Blob: engineGo}, {Path: "a", Mode: ModeFile, Blob: runSh}},
		"file then folder":   {{Path: "a", Mode: ModeFile, Blob: engineGo}, {Path: "a/b", Mode: ModeFile, Blob: runSh}},
		"folder then file":   {{Path: "a/b", Mode: ModeFile, Blob: engineGo}, {Path: "a", Mode: ModeFile, Blob: runSh}},
		"absolute":           {{Path: "/a", Mode: ModeFile, Blob: engineGo}},
		"dot dot":            {{Path: "a/../b", Mode: ModeFile, Blob: engineGo}},
		"dot":                {{Path: "./a", Mode: ModeFile, Blob: engineGo}},
		".git":               {{Path: ".git/config", Mode: ModeFile, Blob: engineGo}},
		".GIT":               {{Path: "a/.GIT/x", Mode: ModeFile, Blob: engineGo}},
		"folder mode":        {{Path: "a", Mode: "040000", Blob: engineGo}},
		"no mode":            {{Path: "a", Blob: engineGo}},
		"short blob":         {{Path: "a", Mode: ModeFile, Blob: "abc"}},
		"upper case blob":    {{Path: "a", Mode: ModeFile, Blob: strings.ToUpper(engineGo)}},
		"no path":            {{Mode: ModeFile, Blob: engineGo}},
		"trailing slash":     {{Path: "a/", Mode: ModeFile, Blob: engineGo}},
		"double slash":       {{Path: "a//b", Mode: ModeFile, Blob: engineGo}},
		"submodule (160000)": {{Path: "a", Mode: "160000", Blob: engineGo}},
	}
	for name, files := range bad {
		if _, err := TreeSHA(files); err == nil {
			t.Errorf("TreeSHA(%s): no error", name)
		}
	}
}

func TestNewSource(t *testing.T) {
	own := []SourceFile{
		{Path: "AGENTS.md", Mode: ModeFile, Blob: runSh},
		{Path: "docs/agents/coder.md", Mode: ModeFile, Blob: runSh},
		{Path: ".theos.yaml", Mode: ModeFile, Blob: runSh},
		{Path: ".devcontainer/devcontainer.json", Mode: ModeFile, Blob: runSh},
		{Path: ".gitea/workflows/ci.yaml", Mode: ModeFile, Blob: runSh},
	}
	got, err := NewSource(giteaActions, slices.Concat(own, engineSource()), "github.com/JairJosafath/cel-lens/engine")
	switch {
	case err != nil:
		t.Fatal(err)
	case got.Tree != sourceTree:
		t.Errorf("Tree = %s, want the tree without the factory's files %s", got.Tree, sourceTree)
	case !reflect.DeepEqual(got.Files, engineSource()):
		t.Errorf("Files = %+v", got.Files)
	case got.GoModule != "github.com/JairJosafath/cel-lens/engine":
		t.Errorf("GoModule = %q", got.GoModule)
	}

	// What only looks like the factory's is the app's.
	app := []SourceFile{
		{Path: "docs/style/guide.md", Mode: ModeFile, Blob: runSh},
		{Path: "docs/agents.md", Mode: ModeFile, Blob: runSh},
		{Path: "src/AGENTS.md", Mode: ModeFile, Blob: runSh},
		{Path: ".gitea-notes", Mode: ModeFile, Blob: runSh},
		{Path: "Makefile", Mode: ModeFile, Blob: runSh},
	}
	if got, err := NewSource(giteaActions, app, ""); err != nil || len(got.Files) != len(app) {
		t.Errorf("NewSource(giteaActions, app files) = %+v, %v, want them all kept", got, err)
	}
	if _, err := NewSource(giteaActions, []SourceFile{{Path: "../x", Mode: ModeFile, Blob: runSh}}, ""); err == nil {
		t.Error("NewSource of a bad path: no error")
	}
}

func TestSourceTag(t *testing.T) {
	core, none := "core-", ""
	tests := []struct {
		source PublishSource
		want   string
	}{
		{PublishSource{Path: "engine"}, "engine/v0.6.1"},
		{PublishSource{Path: "tools/engine"}, "tools/engine/v0.6.1"},
		{PublishSource{Path: "."}, "v0.6.1"},
		{PublishSource{Path: "engine", TagPrefix: &core}, "core-v0.6.1"},
		{PublishSource{Path: "engine", TagPrefix: &none}, "v0.6.1"},
	}
	for _, tt := range tests {
		if got := SourceTag(tt.source, "v0.6.1"); got != tt.want {
			t.Errorf("SourceTag(%+v) = %q, want %q", tt.source, got, tt.want)
		}
	}
	if got := SourceURL("", "JairJosafath/cel-lens", "engine", "engine/v0.6.1"); got !=
		"https://github.com/JairJosafath/cel-lens/tree/engine/v0.6.1/engine" {
		t.Errorf("SourceURL(engine) = %q", got)
	}
	if got := SourceURL("", "JairJosafath/cel-lens", ".", "v0.6.1"); got != "https://github.com/JairJosafath/cel-lens/tree/v0.6.1" {
		t.Errorf("SourceURL(.) = %q", got)
	}
	// #1605: a GitHub Enterprise Server's source is on its own site.
	if got := SourceURL("https://ghe.example.com/", "acme/orders", "orders", "orders/v1.0.0"); got !=
		"https://ghe.example.com/acme/orders/tree/orders/v1.0.0/orders" {
		t.Errorf("SourceURL(ghe) = %q", got)
	}
	if got, want := SourceMessage("CEL engine", "cel-lens/engine", "v0.6.1", "core-v0.6.1"),
		"CEL engine v0.6.1: source\n\ntheos-Source: cel-lens/engine core-v0.6.1\n"; got != want {
		t.Errorf("SourceMessage = %q, want %q", got, want)
	}
}

func TestFoldersOverlap(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"engine", "engine", true},
		{"engine", "engine/core", true},
		{"tools/engine", "tools", true},
		{".", "engine", true},
		{"vscode", ".", true},
		{".", ".", true},
		{"engine", "vscode", false},
		{"engine", "engine2", false},
		{"engine-cli", "engine", false},
		{"tools/a", "tools/b", false},
	}
	for _, tt := range tests {
		if got := FoldersOverlap(tt.a, tt.b); got != tt.want {
			t.Errorf("FoldersOverlap(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}

	self := SourcePlace{Owner: "cel-lens/engine", Repository: "JairJosafath/cel-lens", Path: "engine"}
	others := []SourcePlace{
		self,
		{Owner: "cel-lens/vscode", Repository: "JairJosafath/cel-lens", Path: "vscode"},
		{Owner: "other/all", Repository: "jairjosafath/CEL-LENS", Path: "."},
		{Owner: "cel-lens/core", Repository: "JairJosafath/cel-lens", Path: "engine/core"},
		{Owner: "elsewhere/engine", Repository: "JairJosafath/other", Path: "engine"},
	}
	if got, want := SourceOverlaps(self, others), []string{"cel-lens/core", "other/all"}; !slices.Equal(got, want) {
		t.Errorf("SourceOverlaps = %q, want %q", got, want)
	}
}

func TestGoInstallProblem(t *testing.T) {
	const repo = "JairJosafath/cel-lens"
	tests := []struct {
		name, path, module, tag string
		// want is in the problem; "" is none.
		want string
	}{
		{"no go.mod", "engine", "", "core-v1", ""},
		{"a module in a folder", "engine", "github.com/JairJosafath/cel-lens/engine", "engine/v0.6.1", ""},
		{"a module in a deep folder", "a/b", "github.com/JairJosafath/cel-lens/a/b", "a/b/v1.0.0", ""},
		{"the whole repository", ".", "github.com/JairJosafath/cel-lens", "v0.6.1", ""},
		{"a pre-release", "engine", "github.com/JairJosafath/cel-lens/engine", "engine/v0.7.0-rc.1", ""},
		{"owner and name in another case", "engine", "github.com/jairjosafath/CEL-Lens/engine", "engine/v0.6.1", ""},
		{"a major version suffix", "engine", "github.com/JairJosafath/cel-lens/engine/v2", "engine/v2.0.1", ""},

		{"another module path", "engine", "example.com/cel/engine", "engine/v0.6.1",
			"finds it only as github.com/JairJosafath/cel-lens/engine"},
		{"the folder in another case", "engine", "github.com/JairJosafath/cel-lens/Engine", "engine/v0.6.1",
			"finds it only as"},
		{"the repository's root module for a folder", "engine", "github.com/JairJosafath/cel-lens", "engine/v0.6.1",
			"finds it only as"},
		{"another repository", "engine", "github.com/JairJosafath/cel-lense/engine", "engine/v0.6.1",
			"finds it only as"},
		{"a tag of another shape", "engine", "github.com/JairJosafath/cel-lens/engine", "core-v0.6.1",
			"core-v0.6.1 isn't engine/vX.Y.Z"},
		{"a tag that isn't a version", "engine", "github.com/JairJosafath/cel-lens/engine", "engine/v0.6",
			"isn't engine/vX.Y.Z"},
		{"a prefixed tag of the root", ".", "github.com/JairJosafath/cel-lens", "core-v0.6.1", "isn't vX.Y.Z"},
		{"a major version the suffix doesn't say", "engine", "github.com/JairJosafath/cel-lens/engine/v2",
			"engine/v3.0.0", "asks for v2"},
		{"a major version subfolder", "engine/v2", "github.com/JairJosafath/cel-lens/engine/v2", "engine/v2.1.0", ""},
		{"a major version subfolder tagged as itself", "engine/v2", "github.com/JairJosafath/cel-lens/engine/v2",
			"engine/v2/v2.1.0", "isn't engine/vX.Y.Z"},
		{"a root major version subfolder", "v2", "github.com/JairJosafath/cel-lens/v2", "v2.1.0", ""},
		{"build metadata", "engine", "github.com/JairJosafath/cel-lens/engine", "engine/v1.0.0+meta",
			"isn't engine/vX.Y.Z"},
		{"v2 with no suffix", "engine", "github.com/JairJosafath/cel-lens/engine", "engine/v2.0.0",
			"has no /v2 suffix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GoInstallProblem(repo, tt.path, tt.module, tt.tag)
			if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
				t.Errorf("GoInstallProblem = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNextPublishStepSource(t *testing.T) {
	const (
		tag       = "core-v0.6.1"
		sourceTag = "engine/v0.6.1"
		head      = "1111111111111111111111111111111111111111"
		ours      = "2222222222222222222222222222222222222222"
		module    = "github.com/JairJosafath/cel-lens/engine"
	)
	marker := PublishMarker("cel-lens", "engine", tag)
	asset := ReleaseAsset{ID: 1, Name: "engine-linux", Size: 4096}
	src, err := NewSource(giteaActions, engineSource(), module)
	if err != nil {
		t.Fatal(err)
	}
	draft := func(target string, assets ...ReleaseAsset) TargetRelease {
		return TargetRelease{ID: 9, TagName: tag, Target: target, Draft: true, Body: "notes\n\n" + marker + "\n",
			Assets: assets}
	}
	// read is a release whose runs succeeded, publishing its source to the
	// folder engine, with every read done and change applied.
	read := func(change func(*PublishFacts)) PublishFacts {
		f := PublishFacts{Configured: true, Namespace: "cel-lens", Container: "engine", Tag: "v0.6.1",
			TagPrefix: "core-", App: "CEL engine", Repository: "JairJosafath/cel-lens", Secret: "s",
			SecretFound: true, Runs: RunSucceeded, Source: &PublishSource{Path: "engine"},
			Gitea:        &SourceRelease{Body: "notes\n", Assets: []ReleaseAsset{asset}},
			Remote:       &PublishTarget{Repository: "JairJosafath/cel-lens", ID: 7},
			GitHub:       &TargetReleases{},
			SourceFiles:  src,
			GitHubSource: &GitHubSource{Head: head, HeadTree: emptyTree, HeadBlobs: []string{engineGo}},
		}
		if change != nil {
			change(&f)
		}
		return f
	}
	// out is the source already tagged, on the commit ours.
	out := func(f *PublishFacts) {
		f.GitHubSource = &GitHubSource{Head: ours, HeadTree: sourceTree, TagCommit: ours, TagTree: sourceTree}
	}
	commit := &SourceCommit{Path: "engine", Files: src.Files, Tree: sourceTree, Parent: head,
		Message: "CEL engine v0.6.1: source\n\ntheos-Source: cel-lens/engine core-v0.6.1\n",
		// engine.go's blob is in the head's folder already, and parse.go's is
		// the same blob.
		Upload: []SourceFile{src.Files[0], src.Files[2], src.Files[3], src.Files[5]}}
	big := slices.Clone(src.Files)
	big[1].Size = MaxSourceBlob + 1

	tests := []struct {
		name    string
		facts   PublishFacts
		want    PublishStep
		message string
	}{
		{name: "overlapping folders publish nothing, before anything is read",
			facts: read(func(f *PublishFacts) { f.Overlaps, f.Gitea = []string{"cel-lens/core", "other/all"}, nil }),
			want:  PublishStep{Action: PublishFail, Reason: PublishFolderOverlaps},
			message: "source folder engine of JairJosafath/cel-lens overlaps that of Crew cel-lens/core, other/all; " +
				"nothing is published until one moves"},
		{name: "the GitHub releases are read before the source",
			facts: read(func(f *PublishFacts) { f.GitHub, f.SourceFiles = nil, nil }),
			want:  PublishStep{Action: PublishReadGitHub}},
		{name: "a release tag someone else has is taken before the source is read",
			facts: read(func(f *PublishFacts) { f.GitHub.TagExists, f.SourceFiles = true, nil }),
			want:  PublishStep{Action: PublishFail, Reason: PublishTagTaken}},
		{name: "the source is read from Gitea",
			facts: read(func(f *PublishFacts) { f.SourceFiles, f.GitHubSource = nil, nil }),
			want:  PublishStep{Action: PublishReadSource}},
		{name: "a file over GitHub's blob limit fails",
			facts:   read(func(f *PublishFacts) { f.SourceFiles = &ReleaseSource{Files: big, Tree: sourceTree} }),
			want:    PublishStep{Action: PublishFail, Reason: PublishSourceTooLarge},
			message: "engine.go is 100 MiB, over the 100 MiB a source file may be; the source of v0.6.1 isn't published"},
		{name: "the folder on GitHub is read next",
			facts: read(func(f *PublishFacts) { f.GitHubSource = nil }),
			want:  PublishStep{Action: PublishReadGitHubSource}},
		{name: "a repository with no commit fails",
			facts: read(func(f *PublishFacts) { f.GitHubSource = &GitHubSource{} }),
			want:  PublishStep{Action: PublishFail, Reason: PublishEmptyRepository}, message: "push a first commit"},
		{name: "a source tag on another folder tree is taken",
			facts: read(func(f *PublishFacts) {
				f.GitHubSource = &GitHubSource{Head: head, HeadTree: sourceTree, TagCommit: head, TagTree: cmdTree}
			}),
			want: PublishStep{Action: PublishFail, Reason: PublishSourceTagTaken},
			message: "JairJosafath/cel-lens already has a tag engine/v0.6.1 whose folder engine isn't the source of " +
				"v0.6.1; it leaves it alone"},
		{name: "a source tag on a commit without the folder is taken",
			facts: read(func(f *PublishFacts) { f.GitHubSource.TagCommit = head }),
			want:  PublishStep{Action: PublishFail, Reason: PublishSourceTagTaken}},
		{name: "a folder unlike the source gets a commit, uploading only blobs it lacks",
			facts: read(nil), want: PublishStep{Action: PublishSourceCommit, SourceCommit: commit}},
		{name: "retry: a head whose folder is the source gets the tag",
			facts: read(func(f *PublishFacts) { f.GitHubSource.HeadTree = sourceTree }),
			want:  PublishStep{Action: PublishSourceTag, Tag: sourceTag, Commit: head}},
		{name: "a Go module go install can't find is tagged with a warning",
			facts: read(func(f *PublishFacts) {
				f.GitHubSource.HeadTree = sourceTree
				f.SourceFiles = &ReleaseSource{Files: src.Files, Tree: src.Tree, GoModule: "example.com/engine"}
			}),
			want: PublishStep{Action: PublishSourceTag, Tag: sourceTag, Commit: head,
				Warning: "go.mod declares module example.com/engine, but go install from GitHub finds it only as " +
					"github.com/JairJosafath/cel-lens/engine"}},
		{name: "retry: a tagged source makes the draft on its commit",
			facts: read(out),
			want: PublishStep{Action: PublishCreateDraft, Tag: tag, Name: "CEL engine v0.6.1",
				Body: "notes\n\n" + marker + "\n", Target: ours}},
		{name: "retry: a source tagged on an older head still counts",
			facts: read(func(f *PublishFacts) {
				f.GitHubSource = &GitHubSource{Head: head, HeadTree: cmdTree, TagCommit: ours, TagTree: sourceTree}
			}),
			want: PublishStep{Action: PublishCreateDraft, Tag: tag, Name: "CEL engine v0.6.1",
				Body: "notes\n\n" + marker + "\n", Target: ours}},
		{name: "a draft made before source was set is retargeted",
			facts: read(func(f *PublishFacts) { out(f); f.GitHub = &TargetReleases{Releases: []TargetRelease{draft("main")}} }),
			want:  PublishStep{Action: PublishRetargetDraft, Release: 9, Target: ours}},
		{name: "a draft on the source commit gets its files",
			facts: read(func(f *PublishFacts) { out(f); f.GitHub = &TargetReleases{Releases: []TargetRelease{draft(ours)}} }),
			want:  PublishStep{Action: PublishUploadAsset, Release: 9, Asset: asset}},
		{name: "a release tag that is the source tag is the release's to take",
			facts: read(func(f *PublishFacts) {
				core := "core-"
				f.Source.TagPrefix = &core
				f.GitHub.TagExists = true
				out(f)
			}),
			want: PublishStep{Action: PublishCreateDraft, Tag: tag, Name: "CEL engine v0.6.1",
				Body: "notes\n\n" + marker + "\n", Target: ours}},
		{name: "a published release is done with its source",
			facts: read(func(f *PublishFacts) {
				out(f)
				f.GitHub = &TargetReleases{TagExists: true, Releases: []TargetRelease{{ID: 9, TagName: tag, Target: ours,
					Body: marker, URL: "https://github.com/JairJosafath/cel-lens/releases/tag/core-v0.6.1"}}}
			}),
			want: PublishStep{Action: PublishDone, Reason: PublishPublished,
				URL: "https://github.com/JairJosafath/cel-lens/releases/tag/core-v0.6.1",
				Source: &PublishedSource{Commit: ours, Tag: sourceTag,
					URL: "https://github.com/JairJosafath/cel-lens/tree/engine/v0.6.1/engine"}},
			message: "its source at https://github.com/JairJosafath/cel-lens/tree/engine/v0.6.1/engine"},
		{name: "a published release reads the source to say where it is",
			facts: read(func(f *PublishFacts) {
				f.GitHub = &TargetReleases{Releases: []TargetRelease{{ID: 9, TagName: tag, Body: marker}}}
				f.SourceFiles, f.GitHubSource = nil, nil
			}),
			want: PublishStep{Action: PublishReadSource}},
		{name: "a published release reads the source tag to say where its source is",
			facts: read(func(f *PublishFacts) {
				f.GitHub = &TargetReleases{Releases: []TargetRelease{{ID: 9, TagName: tag, Body: marker}}}
				f.GitHubSource = nil
			}),
			want: PublishStep{Action: PublishReadGitHubSource}},
		{name: "a published release isn't done with a source tag someone else made",
			facts: read(func(f *PublishFacts) {
				f.GitHub = &TargetReleases{Releases: []TargetRelease{{ID: 9, TagName: tag, Body: marker, URL: "u"}}}
				f.GitHubSource.TagCommit, f.GitHubSource.TagTree = ours, cmdTree
			}),
			want: PublishStep{Action: PublishDone, Reason: PublishPublished, URL: "u"}},
		{name: "a source of only the factory's own files publishes nothing",
			facts:   read(func(f *PublishFacts) { f.SourceFiles = &ReleaseSource{Tree: emptyTree} }),
			want:    PublishStep{Action: PublishFail, Reason: PublishNoReleaseFiles},
			message: "holds only the factory's own files"},
		{name: "a release published before source was set is done without",
			facts: read(func(f *PublishFacts) {
				f.GitHub = &TargetReleases{Releases: []TargetRelease{{ID: 9, TagName: tag, Body: marker, URL: "u"}}}
			}),
			want: PublishStep{Action: PublishDone, Reason: PublishPublished, URL: "u"}},

		{name: "a protected default branch fails",
			facts: read(func(f *PublishFacts) { f.Failure = &PublishFailure{Reason: PublishBranchProtected} }),
			want:  PublishStep{Action: PublishFail, Reason: PublishBranchProtected},
			message: "the default branch of JairJosafath/cel-lens is protected, so the source of v0.6.1 can't go " +
				"on it; let the token's user push to it, or drop the protection"},
		{name: "a blob GitHub refuses as too large fails",
			facts: read(func(f *PublishFacts) {
				f.Failure = &PublishFailure{Reason: PublishSourceTooLarge, Detail: "HTTP 422"}
			}),
			want: PublishStep{Action: PublishFail, Reason: PublishSourceTooLarge}, message: "too large: HTTP 422"},
		{name: "another app's commit landing first is no failure",
			facts: read(func(f *PublishFacts) { f.Failure = &PublishFailure{Reason: PublishBranchMoved} }),
			want:  PublishStep{Action: PublishWait, Reason: PublishWaiting}, message: "the next poll puts the source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextPublishStep(tt.facts)
			if !strings.Contains(got.Message, tt.message) {
				t.Errorf("message = %q, want it to contain %q", got.Message, tt.message)
			}
			got.Message = ""
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextPublishStep() =\n%+v, want\n%+v", got, tt.want)
			}
		})
	}
}

// fakeSourceRepo is a GitHub repository holding a source folder: its commits,
// each with its folder's tree and blobs, the default branch, the tags and the
// releases.
type fakeSourceRepo struct {
	head     string
	commits  map[string]fakeSourceCommit
	tags     map[string]string
	releases []TargetRelease
	// made counts the source commits the operator made.
	made int
	// race lands another app's commit, raced, on the branch before the first
	// fast-forward.
	race  bool
	raced string
}

type fakeSourceCommit struct {
	parent, tree string
	blobs        []string
}

func (r *fakeSourceRepo) commit(parent, tree string, blobs []string) string {
	sha := fmt.Sprintf("%040d", len(r.commits)+1)
	r.commits[sha] = fakeSourceCommit{parent: parent, tree: tree, blobs: blobs}
	return sha
}

// take takes a step that writes, and returns how it failed, or nil.
func (r *fakeSourceRepo) take(t *testing.T, step PublishStep, blobs []string) *PublishFailure {
	t.Helper()
	switch step.Action {
	case PublishSourceCommit:
		c := step.SourceCommit
		for _, u := range c.Upload {
			if slices.Contains(r.commits[r.head].blobs, u.Blob) {
				t.Fatalf("uploaded %s, a blob the head has", u.Path)
			}
		}
		if c.Tree != sourceTree || c.Path != "engine" {
			t.Fatalf("commit %+v", c)
		}
		made := r.commit(c.Parent, c.Tree, blobs)
		if r.race && r.raced == "" {
			// Another app's folder changes; this one's doesn't.
			r.raced = r.commit(r.head, cmdTree, []string{engineGo})
			r.head = r.raced
		}
		if r.head != c.Parent {
			return &PublishFailure{Reason: PublishBranchMoved}
		}
		r.head = made
		r.made++
	case PublishSourceTag:
		r.tags[step.Tag] = step.Commit
	case PublishRetargetDraft:
		r.releases[0].Target = step.Target
	case PublishCreateDraft:
		r.releases = append(r.releases, TargetRelease{ID: 7, TagName: step.Tag, Target: step.Target,
			Draft: true, Body: step.Body})
	case PublishUploadAsset:
		r.releases[0].Assets = append(r.releases[0].Assets, step.Asset)
	case PublishDraft:
		r.releases[0].Draft, r.releases[0].URL = false, "https://github.com/o/r/releases/tag/core-v0.6.1"
		r.tags["core-v0.6.1"] = r.releases[0].Target
	case PublishWait:
	default:
		t.Fatalf("step %+v", step)
	}
	return nil
}

func (r *fakeSourceRepo) read() *GitHubSource {
	h := r.commits[r.head]
	gh := &GitHubSource{Head: r.head, HeadTree: h.tree, HeadBlobs: h.blobs}
	if c, ok := r.tags["engine/v0.6.1"]; ok {
		gh.TagCommit, gh.TagTree = c, r.commits[c].tree
	}
	return gh
}

// TestNextPublishStepSourceRuns publishes a release and its source on a fake
// of both sides, from nothing on GitHub and from every half-done state, and
// with another app's commit landing first.
func TestNextPublishStepSourceRuns(t *testing.T) {
	src, err := NewSource(giteaActions, engineSource(), "github.com/o/r/engine")
	if err != nil {
		t.Fatal(err)
	}
	blobs := make([]string, 0, len(src.Files))
	for _, f := range src.Files {
		blobs = append(blobs, f.Blob)
	}
	asset := ReleaseAsset{ID: 1, Name: "engine-linux", Size: 10}
	marker := PublishMarker("ns", "engine", "core-v0.6.1")

	tests := []struct {
		name string
		// setup puts the repository in a half-done state.
		setup func(r *fakeSourceRepo)
		// race lands another app's commit on the branch before the first
		// fast-forward.
		race bool
		// commits is the source commits the publish makes.
		commits int
	}{
		{name: "nothing on GitHub", setup: func(*fakeSourceRepo) {}, commits: 1},
		// Blobs, trees and a commit nothing names are invisible: a retry
		// builds them again, and they are the same objects.
		{name: "blobs up, tree made, commit made", setup: func(r *fakeSourceRepo) {
			r.commit(r.head, sourceTree, blobs)
		}, commits: 1},
		{name: "branch moved", setup: func(r *fakeSourceRepo) {
			r.head = r.commit(r.head, sourceTree, blobs)
		}},
		{name: "tag made", setup: func(r *fakeSourceRepo) {
			r.head = r.commit(r.head, sourceTree, blobs)
			r.tags["engine/v0.6.1"] = r.head
		}},
		{name: "tag made, another app's commit on top", setup: func(r *fakeSourceRepo) {
			c := r.commit(r.head, sourceTree, blobs)
			r.tags["engine/v0.6.1"] = c
			r.head = r.commit(c, sourceTree, blobs)
		}},
		{name: "a draft made before source was set", setup: func(r *fakeSourceRepo) {
			r.releases = []TargetRelease{{ID: 7, TagName: "core-v0.6.1", Target: "main", Draft: true,
				Body: "notes\n\n" + marker + "\n", Assets: []ReleaseAsset{asset}}}
		}, commits: 1},
		{name: "another app's commit lands first", setup: func(*fakeSourceRepo) {}, race: true, commits: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeSourceRepo{commits: map[string]fakeSourceCommit{}, tags: map[string]string{}, race: tt.race}
			// The head's folder holds an older engine.go.
			r.head = r.commit("", cmdTree, []string{engineGo})
			tt.setup(r)
			var failure *PublishFailure
			var actions []PublishAction
			for range 20 {
				f := PublishFacts{Configured: true, Namespace: "ns", Container: "engine", Tag: "v0.6.1",
					TagPrefix: "core-", Repository: "o/r", Secret: "s", SecretFound: true, Runs: RunSucceeded,
					Source: &PublishSource{Path: "engine"}, Failure: failure}
				failure = nil
				for {
					step := NextPublishStep(f)
					switch step.Action {
					case PublishReadGitea:
						f.Gitea = &SourceRelease{Body: "notes\n", Assets: []ReleaseAsset{asset}}
						continue
					case PublishReadRepository:
						f.Remote = &PublishTarget{Repository: f.Repository, ID: 7}
						continue
					case PublishReadGitHub:
						_, taken := r.tags["core-v0.6.1"]
						f.GitHub = &TargetReleases{Releases: slices.Clone(r.releases), TagExists: taken}
						continue
					case PublishReadSource:
						f.SourceFiles = src
						continue
					case PublishReadGitHubSource:
						f.GitHubSource = r.read()
						continue
					}
					actions = append(actions, step.Action)
					if step.Action != PublishDone {
						failure = r.take(t, step, blobs)
						break
					}
					switch c := r.tags["engine/v0.6.1"]; {
					case r.commits[c].tree != sourceTree:
						t.Fatalf("the source tag's folder is %s", r.commits[c].tree)
					case r.tags["core-v0.6.1"] != c:
						t.Fatalf("the release's tag is on %s, the source's on %s", r.tags["core-v0.6.1"], c)
					case step.Source == nil || step.Source.Commit != c || step.Source.Tag != "engine/v0.6.1":
						t.Fatalf("done with source %+v", step.Source)
					case r.made != tt.commits:
						t.Fatalf("made %d source commits, want %d", r.made, tt.commits)
					case tt.race && (r.commits[c].parent != r.raced || !slices.Contains(actions, PublishWait)):
						t.Fatalf("after the race, the source commit's parent is %s, want %s; steps %v",
							r.commits[c].parent, r.raced, actions)
					}
					return
				}
			}
			t.Fatalf("not published after 20 polls: %v", actions)
		})
	}
}

// NextSourceStep is the source rule the github adapter asks (#1598): it reads
// GitHub first, then commits the folder, then tags the commit.
func TestNextSourceStep(t *testing.T) {
	files := &ReleaseSource{Files: []SourceFile{{Path: "main.go", Mode: "100644", Size: 3, Blob: "b1"}}, Tree: "t1"}
	s := SourceShip{Source: PublishSource{Path: "."}, Files: files, Repository: "o/r", App: "shop",
		Owner: "ns/api", Tag: "v1.0.0", ReleaseTag: "api-v1.0.0"}
	if step, done := NextSourceStep(s, nil); step.Action != PublishReadGitHubSource || done != nil {
		t.Errorf("unread: got %v, %v", step.Action, done)
	}
	if step, _ := NextSourceStep(s, &GitHubSource{Head: "h1", HeadTree: "t0"}); step.Action != PublishSourceCommit ||
		step.SourceCommit.Parent != "h1" || len(step.SourceCommit.Upload) != 1 {
		t.Errorf("behind: got %+v", step)
	}
	if step, _ := NextSourceStep(s, &GitHubSource{Head: "h2", HeadTree: "t1"}); step.Action != PublishSourceTag ||
		step.Commit != "h2" || step.Tag != SourceTag(s.Source, s.Tag) {
		t.Errorf("committed: got %+v", step)
	}
	if _, done := NextSourceStep(s, &GitHubSource{Head: "h2", HeadTree: "t1", TagCommit: "h2", TagTree: "t1"}); done == nil ||
		done.Commit != "h2" {
		t.Errorf("tagged: got %+v", done)
	}
}
