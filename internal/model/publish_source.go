package model

import (
	"crypto/sha1" //nolint:gosec // git names its objects by SHA-1; this is no security check
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// FactoryOwnPaths returns the factory's own files in a Container's repository
// on the CI system ci: those no agent may change and those the operator and
// the CI system own, sorted. A published source leaves them out; everything
// else at the tag is the app's.
func FactoryOwnPaths(ci CI) []string { return AgentProtectedPaths(ci, AgentNeverPaths) }

// MaxSourceBlob is the largest file GitHub takes as a blob, 100 MiB.
const MaxSourceBlob = 100 << 20

// FileMode is a file's mode in a git tree.
type FileMode string

// The modes a file of a source has.
const (
	ModeFile       FileMode = "100644"
	ModeExecutable FileMode = "100755"
	ModeSymlink    FileMode = "120000"
)

// emptyTreeSHA is the git tree with nothing in it.
const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// modeTree is a folder's mode inside a tree object; git mktree reads it as
// 040000 too.
const modeTree = "40000"

// PublishSource is the folder of the GitHub repository a Crew publishes its
// releases' source to, from publishReleases.source.
type PublishSource struct {
	// Path is the folder; "." is the whole repository.
	Path string
	// TagPrefix goes before the Crew's tag in the source tag; nil takes the
	// default, SourceTagPrefix.
	TagPrefix *string
}

// SourceFile is a file of a release's source: its path from the repository's
// root, its mode, its size and its git blob SHA-1. A symlink's content is its
// target.
type SourceFile struct {
	Path string
	Mode FileMode
	Size int64
	Blob string
}

// ReleaseSource is a release's source as published: the Container's files at
// the tag without the factory's own, and the git tree they make.
type ReleaseSource struct {
	Files []SourceFile
	// Tree is the git tree SHA-1 of Files.
	Tree string
	// GoModule is the module path go.mod at the root declares, or "" for a
	// source with no go.mod.
	GoModule string
	// Problems stop the source going out (PublishChecks); Allowed counts the
	// lines AllowSecretMark skipped. Check sets both.
	Problems []PublishProblem
	Allowed  int
}

// GitHubSource is what the GitHub repository holds of a Crew's source folder.
// A SHA is "" for a thing that isn't there.
type GitHubSource struct {
	// Head is the default branch's head commit; HeadTree is the folder's tree
	// at it, and HeadBlobs the blobs in that tree, at any depth.
	Head, HeadTree string
	HeadBlobs      []string
	// TagCommit is the commit the source tag names, and TagTree the folder's
	// tree at it.
	TagCommit, TagTree string
}

// SourceCommit is a commit of a release's source the operator builds: it
// uploads the Upload blobs, makes the folder's tree of Files (which must come
// back as Tree) and the root tree, the head's with only Path replaced, makes
// the commit on Parent with Message, and fast-forwards the default branch to it.
type SourceCommit struct {
	Path    string
	Files   []SourceFile
	Upload  []SourceFile
	Tree    string
	Parent  string
	Message string
}

// PublishedSource is a release's source on GitHub: the commit, the source tag
// and the folder at the tag.
type PublishedSource struct {
	Commit, Tag, URL string
}

// SourcePlace is where a Crew, or a Container through a github DeployTarget,
// publishes its source: its namespace/name, the repository and the folder.
type SourcePlace struct {
	Owner, Repository, Path string
}

// NewSource keeps the files of a Container's repository at a tag that are the
// app's, all but FactoryOwnPaths of its CI system ci, and hashes them into a git tree. goModule is
// the module path the root go.mod declares, or "". It fails on files no git
// tree can hold: a bad path or mode, one path twice, or a file where a folder
// is.
func NewSource(ci CI, files []SourceFile, goModule string) (*ReleaseSource, error) {
	own := FactoryOwnPaths(ci)
	var kept []SourceFile
	for _, f := range files {
		if !coveredByAny(own, f.Path) {
			kept = append(kept, f)
		}
	}
	tree, err := TreeSHA(kept)
	if err != nil {
		return nil, err
	}
	return &ReleaseSource{Files: kept, Tree: tree, GoModule: goModule}, nil
}

// BlobSHA is the git blob SHA-1 of content, as git hash-object prints it.
func BlobSHA(content []byte) string {
	h := blobHasher(int64(len(content)))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// HashBlob is the git blob SHA-1 of the size bytes r holds, read as a stream.
func HashBlob(r io.Reader, size int64) (string, error) {
	h := blobHasher(size)
	n, err := io.Copy(h, r)
	switch {
	case err != nil:
		return "", err
	case n != size:
		return "", fmt.Errorf("read %d bytes of a %d byte file", n, size)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func blobHasher(size int64) hash.Hash {
	h := sha1.New() //nolint:gosec // git's object name
	h.Write(fmt.Appendf(nil, "blob %d\x00", size))
	return h
}

// treeNode is a folder while TreeSHA builds it: its files and its folders.
type treeNode struct {
	files   map[string]SourceFile
	folders map[string]*treeNode
}

// TreeSHA is the git tree SHA-1 of files, as git mktree prints it for the root
// of a repository holding them. Empty folders don't exist in git; no files is
// the empty tree.
func TreeSHA(files []SourceFile) (string, error) {
	root := &treeNode{files: map[string]SourceFile{}, folders: map[string]*treeNode{}}
	for _, f := range files {
		if err := root.add(f); err != nil {
			return "", err
		}
	}
	return root.sha(), nil
}

func (n *treeNode) add(f SourceFile) error {
	if !validSourcePath(f.Path) {
		return fmt.Errorf("%q is no path a git tree can hold", f.Path)
	}
	switch f.Mode {
	case ModeFile, ModeExecutable, ModeSymlink:
	default:
		return fmt.Errorf("%s has mode %q, not a file's, an executable's or a symlink's", f.Path, f.Mode)
	}
	if len(f.Blob) != 40 || strings.Trim(f.Blob, "0123456789abcdef") != "" {
		return fmt.Errorf("%s has blob %q, not a SHA-1", f.Path, f.Blob)
	}
	segs := strings.Split(f.Path, "/")
	for i, s := range segs[:len(segs)-1] {
		if _, ok := n.files[s]; ok {
			return fmt.Errorf("%s is a file and a folder", strings.Join(segs[:i+1], "/"))
		}
		next := n.folders[s]
		if next == nil {
			next = &treeNode{files: map[string]SourceFile{}, folders: map[string]*treeNode{}}
			n.folders[s] = next
		}
		n = next
	}
	name := segs[len(segs)-1]
	if _, ok := n.files[name]; ok {
		return fmt.Errorf("%s is in the source twice", f.Path)
	}
	if _, ok := n.folders[name]; ok {
		return fmt.Errorf("%s is a file and a folder", f.Path)
	}
	n.files[name] = f
	return nil
}

// sha writes the tree object git writes: its entries sorted by name, a
// folder's as if it ended in "/", each "<mode> <name>\0" and the binary SHA-1.
func (n *treeNode) sha() string {
	type entry struct{ key, mode, name, sha string }
	entries := make([]entry, 0, len(n.files)+len(n.folders))
	for name, f := range n.files {
		entries = append(entries, entry{name, string(f.Mode), name, f.Blob})
	}
	for name, sub := range n.folders {
		entries = append(entries, entry{name + "/", modeTree, name, sub.sha()})
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.key, b.key) })
	var body []byte
	for _, e := range entries {
		raw, _ := hex.DecodeString(e.sha)
		body = append(body, e.mode+" "+e.name+"\x00"...)
		body = append(body, raw...)
	}
	h := sha1.New() //nolint:gosec // git's object name
	h.Write(fmt.Appendf(nil, "tree %d\x00", len(body)))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// validSourcePath says a file's path is relative, with no empty, "." or ".."
// segment and no .git segment.
func validSourcePath(p string) bool {
	if p == "" {
		return false
	}
	for s := range strings.SplitSeq(p, "/") {
		if s == "" || s == "." || s == ".." || strings.EqualFold(s, ".git") {
			return false
		}
	}
	return true
}

// SourceTagPrefix is what goes before the Crew's tag in the source tag: prefix
// when set, else what Go asks of a module in a folder, the path and "/", and
// nothing for the whole repository.
func SourceTagPrefix(path string, prefix *string) string {
	switch {
	case prefix != nil:
		return *prefix
	case path == ".":
		return ""
	}
	return path + "/"
}

// SourceTag is the tag a release's source goes out under on GitHub.
func SourceTag(s PublishSource, tag string) string {
	return SourceTagPrefix(s.Path, s.TagPrefix) + tag
}

// SourceURL is the source folder at its tag on GitHub, web its site
// (github.com when "").
func SourceURL(web, repository, path, tag string) string {
	if web == "" {
		web = GitHubWeb
	}
	url := strings.TrimRight(web, "/") + "/" + repository + "/tree/" + tag
	if path != "." {
		url += "/" + path
	}
	return url
}

// SourceMessage is the message of a source commit: the release's name, and a
// trailer naming its owner, the Container as namespace/name, and the GitHub tag.
func SourceMessage(app, owner, crewTag, tag string) string {
	return fmt.Sprintf("%s: source\n\nC4factory-Source: %s %s\n", ReleaseName(app, crewTag), owner, tag)
}

// FoldersOverlap says two source folders of one repository are equal, or one
// is inside the other; "." holds every folder.
func FoldersOverlap(a, b string) bool {
	if a == "." || b == "." || a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// SourceOverlaps names the other owners, sorted, that publish source to the
// same repository as self, owner/name in any case, in a folder overlapping
// self's.
func SourceOverlaps(self SourcePlace, others []SourcePlace) []string {
	var out []string
	for _, o := range others {
		if o.Owner != self.Owner && strings.EqualFold(o.Repository, self.Repository) && FoldersOverlap(o.Path, self.Path) {
			out = append(out, o.Owner)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// sourceTagVersion is the version part of a tag Go reads as a module version.
var sourceTagVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

// majorSuffix is a module path's major version suffix, /v2 and up.
var majorSuffix = regexp.MustCompile(`/v([2-9]|[1-9][0-9]+)$`)

// GoInstallProblem says why go install can't fetch a source's Go module from
// GitHub, or "" when it can or the source has no go.mod. Go looks for a module
// in a folder of the repository at github.com/<repository>/<path>, a /vN
// suffix allowed, under the tag <path>/vX.Y.Z, vX.Y.Z for the whole repository,
// with major version N when the path ends in /vN; a folder that ends in /vN
// too is a major version subfolder, tagged as the folder above it.
func GoInstallProblem(repository, path, module, tag string) string {
	if module == "" {
		return ""
	}
	repo, folder, tagPrefix := "github.com/"+repository, "", ""
	if path != "." {
		folder, tagPrefix = "/"+path, path+"/"
	}
	base, major := module, ""
	if m := majorSuffix.FindStringSubmatch(module); m != nil {
		base, major = strings.TrimSuffix(module, "/v"+m[1]), m[1]
		// A major version kept in its own folder, engine/v2, is tagged as
		// the folder above it: engine/v2.0.0.
		if above, ok := strings.CutSuffix(folder, "/v"+major); ok {
			folder, tagPrefix = above, strings.TrimPrefix(above+"/", "/")
		}
	}
	// GitHub's owner and name are any case; the folder is as it is.
	if len(base) != len(repo)+len(folder) || !strings.EqualFold(base[:len(repo)], repo) || base[len(repo):] != folder {
		return fmt.Sprintf("go.mod declares module %s, but go install from GitHub finds it only as %s", module, repo+folder)
	}
	version, ok := strings.CutPrefix(tag, tagPrefix)
	m := sourceTagVersion.FindStringSubmatch(version)
	if !ok || m == nil {
		return fmt.Sprintf("the source tag %s isn't %svX.Y.Z, the tag go install of %s looks for", tag, tagPrefix, module)
	}
	got, _ := strconv.Atoi(m[1])
	switch {
	case major != "" && m[1] != major:
		return fmt.Sprintf("the source tag %s has major version %s, but module %s asks for v%s", tag, m[1], module, major)
	case major == "" && got > 1:
		return fmt.Sprintf("the source tag %s has major version %d, but module %s has no /v%d suffix", tag, got, module, got)
	}
	return ""
}

// sourceStep publishes the release's source, or says it is out: done is the
// source commit and tag once both are there, and nil while step is to be taken.
func sourceStep(f PublishFacts, tag string) (step PublishStep, done *PublishedSource) {
	if f.SourceFiles == nil {
		return PublishStep{Action: PublishReadSource}, nil
	}
	return NextSourceStep(SourceShip{Source: *f.Source, Files: f.SourceFiles, Repository: f.Repository,
		App: f.App, Owner: f.Namespace + "/" + f.Container, Tag: f.Tag, ReleaseTag: tag}, f.GitHubSource)
}

// SourceShip is a release's source shipped to a GitHub repository: where it
// goes, the files read from the release's tag, and the release it is the
// source of (its version tag and its tag on GitHub, for the commit message).
// Owner is the Container the release is of, as namespace/name. Web is the
// GitHub site the repository is on, github.com when "".
type SourceShip struct {
	Source     PublishSource
	Files      *ReleaseSource
	Web        string
	Repository string
	App        string
	Owner      string
	Tag        string
	ReleaseTag string
}

// NextSourceStep is the next write that publishes s's source, given gh, what
// GitHub holds at the repository's head and source tag (nil before it is
// read): the source commit, then its tag. done is the source commit and tag
// once both are there, and nil while step is to be taken.
func NextSourceStep(s SourceShip, gh *GitHubSource) (step PublishStep, done *PublishedSource) {
	src := s.Source
	sourceTag := SourceTag(src, s.Tag)
	for _, file := range s.Files.Files {
		if file.Size > MaxSourceBlob {
			return fail(PublishSourceTooLarge, "%s is %d MiB, over the 100 MiB a source file may be; the source of %s isn't published",
				file.Path, file.Size>>20, s.Tag), nil
		}
	}
	switch {
	case s.Files.Tree == emptyTreeSHA:
		// GitHub holds no empty folder, so a commit of it would be made again
		// every poll.
		return fail(PublishNoReleaseFiles, "the source of %s holds only the factory's own files; nothing is published",
			s.Tag), nil
	case gh == nil:
		return PublishStep{Action: PublishReadGitHubSource}, nil
	case gh.Head == "":
		return fail(PublishEmptyRepository, "%s has no default branch to put the source of %s on; push a first commit",
			s.Repository, s.Tag), nil
	case gh.TagCommit != "" && gh.TagTree == s.Files.Tree:
		return PublishStep{}, &PublishedSource{Commit: gh.TagCommit, Tag: sourceTag,
			URL: SourceURL(s.Web, s.Repository, src.Path, sourceTag)}
	case len(s.Files.Problems) > 0:
		// Before any write: a release the agents made doesn't go out by
		// itself with a secret in it (#1602).
		return fail(s.Files.Problems[0].Reason, "the source of %s isn't published: %s; a newer release publishes "+
			"instead", s.Tag, PublishProblemsMessage(s.Files.Problems, s.Files.Allowed)), nil
	case gh.TagCommit != "":
		return fail(PublishSourceTagTaken, "%s already has a tag %s whose %s isn't the source of %s; it leaves it alone",
			s.Repository, sourceTag, folderName(src.Path), s.Tag), nil
	case gh.HeadTree == s.Files.Tree:
		return PublishStep{Action: PublishSourceTag, Tag: sourceTag, Commit: gh.Head,
			Warning: GoInstallProblem(s.Repository, src.Path, s.Files.GoModule, sourceTag)}, nil
	}
	var upload []SourceFile
	for _, file := range s.Files.Files {
		if !slices.Contains(gh.HeadBlobs, file.Blob) &&
			!slices.ContainsFunc(upload, func(u SourceFile) bool { return u.Blob == file.Blob }) {
			upload = append(upload, file)
		}
	}
	return PublishStep{Action: PublishSourceCommit, SourceCommit: &SourceCommit{
		Path: src.Path, Files: s.Files.Files, Upload: upload, Tree: s.Files.Tree, Parent: gh.Head,
		Message: SourceMessage(s.App, s.Owner, s.Tag, s.ReleaseTag),
	}}, nil
}

// folderName is the source folder, for a message.
func folderName(path string) string {
	if path == "." {
		return "root"
	}
	return "folder " + path
}
