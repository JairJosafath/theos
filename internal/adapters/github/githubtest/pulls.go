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

package githubtest

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// The fake's pull requests, step 2 of docs/plans/github-host.md (#75). They
// are real ones on the bare repositories: a merge is Git's squash of the
// head onto the base, update-branch its merge of the base into the head. Each
// call answers as GitHub documents it:
//
//	POST  /repos/{o}/{r}/pulls                    open one; 422 when the head has an open one, or has no commit the base lacks
//	GET   /repos/{o}/{r}/pulls?head=o:b&state=    the pull requests of a head
//	GET   /repos/{o}/{r}/pulls/{n}                one, with mergeable, the size of its change and, once merged, its commit
//	PATCH /repos/{o}/{r}/pulls/{n}                close it
//	GET   /repos/{o}/{r}/pulls/{n}/files          its files, a renamed one with previous_filename
//	GET   /repos/{o}/{r}/pulls/{n}/commits        its commits, oldest first, also once merged and its branch deleted
//	PUT   /repos/{o}/{r}/pulls/{n}/merge          merge it by squash; 409 when sha is not its head, 405 when it conflicts
//	PUT   /repos/{o}/{r}/pulls/{n}/update-branch  merge the base into its branch (202); 422 on a conflict, and when the
//	                                              base has nothing new
//	POST  /repos/{o}/{r}/pulls/{n}/reviews        review a commit; 422 when its author approves or asks for changes
//	GET   /repos/{o}/{r}/pulls/{n}/reviews        its reviews, oldest first
//	GET   /repos/{o}/{r}/compare/{base}...{head}  how far head is ahead of and behind base
//
// https://docs.github.com/en/rest/pulls/pulls, /rest/pulls/reviews and
// /rest/commits/commits#compare-two-commits. A pull request has a number
// among the repository's issues, so a comment on it is an issue comment
// (issues.go). GitHub works out mergeable in the background and answers null
// meanwhile; the fake answers at once.

type pull struct {
	number      int
	title, body string
	head, base  string
	author      string
	state       string
	merged      bool
	mergeCommit string
	// headSHA is the head's commit, followed while the branch exists; forkPoint
	// is where the head left the base, kept from the merge on.
	headSHA   string
	forkPoint string
	reviews   []*pullReview
}

type pullReview struct {
	ID     int64  `json:"id"`
	State  string `json:"state"`
	Commit string `json:"commit_id"`
	Body   string `json:"body"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
}

var (
	pullPath        = regexp.MustCompile(`^/pulls/(\d+)$`)
	pullFilesPath   = regexp.MustCompile(`^/pulls/(\d+)/files$`)
	pullCommitsPath = regexp.MustCompile(`^/pulls/(\d+)/commits$`)
	pullMergePath   = regexp.MustCompile(`^/pulls/(\d+)/merge$`)
	pullUpdatePath  = regexp.MustCompile(`^/pulls/(\d+)/update-branch$`)
	pullReviewsPath = regexp.MustCompile(`^/pulls/(\d+)/reviews$`)
	comparePath     = regexp.MustCompile(`^/compare/(.+)\.\.\.(.+)$`)
)

const pullsRest = "/pulls"

// approve is the review event that approves.
const approve = "APPROVE"

// DismissReview dismisses the review with id on a pull request, as a
// maintainer does: its state becomes DISMISSED.
func (s *Server) DismissReview(owner, name string, number int, id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.repos[owner+"/"+name].pulls[number]; p != nil {
		for _, rv := range p.reviews {
			if rv.ID == id {
				rv.State = "DISMISSED"
			}
		}
	}
}

// AddPendingReview leaves a review of user on a pull request as a draft not
// yet submitted, and returns its id.
func (s *Server) AddPendingReview(owner, name string, number int, user, body string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.repos[owner+"/"+name].pulls[number]
	s.nextID++
	rv := &pullReview{ID: s.nextID, State: "PENDING", Commit: p.headSHA, Body: body}
	rv.User.Login = user
	p.reviews = append(p.reviews, rv)
	return rv.ID
}

// Reviews returns the ids of a pull request's reviews, oldest first.
func (s *Server) Reviews(owner, name string, number int) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	if p := s.repos[owner+"/"+name].pulls[number]; p != nil {
		for _, rv := range p.reviews {
			ids = append(ids, rv.ID)
		}
	}
	return ids
}

// pullsAPI serves the calls of this file, and says whether rest was one.
func (s *Server) pullsAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) bool {
	if rest == pullsRest {
		switch r.Method {
		case http.MethodPost:
			s.openPull(w, r, kept)
		case http.MethodGet:
			s.listPulls(w, r, kept)
		default:
			return false
		}
		return true
	}
	if m := comparePath.FindStringSubmatch(rest); m != nil && r.Method == http.MethodGet {
		s.compare(w, kept, m[1], m[2])
		return true
	}
	for _, re := range []*regexp.Regexp{pullPath, pullFilesPath, pullCommitsPath, pullMergePath, pullUpdatePath,
		pullReviewsPath} {
		m := re.FindStringSubmatch(rest)
		if m == nil {
			continue
		}
		number, _ := strconv.Atoi(m[1])
		p := kept.pulls[number]
		if p == nil {
			reply(w, http.StatusNotFound, message("Not Found"))
			return true
		}
		s.follow(kept, p)
		switch {
		case re == pullPath && r.Method == http.MethodGet:
			reply(w, http.StatusOK, s.pullJSON(kept, p))
		case re == pullPath && r.Method == http.MethodPatch:
			s.patchPull(w, r, kept, p)
		case re == pullFilesPath && r.Method == http.MethodGet:
			s.pullFiles(w, r, kept, p)
		case re == pullCommitsPath && r.Method == http.MethodGet:
			s.pullCommits(w, r, kept, p)
		case re == pullMergePath && r.Method == http.MethodPut:
			s.mergePull(w, r, kept, p)
		case re == pullUpdatePath && r.Method == http.MethodPut:
			s.updatePull(w, kept, p)
		case re == pullReviewsPath && r.Method == http.MethodPost:
			s.reviewPull(w, r, p)
		case re == pullReviewsPath && r.Method == http.MethodGet:
			reply(w, http.StatusOK, page(r.URL.Query(), append([]*pullReview{}, p.reviews...)))
		default:
			return false
		}
		return true
	}
	return false
}

func (s *Server) branch(kept *repo, name string) (string, bool) {
	sha, _, ok := s.commit(kept.owner, kept.name, "refs/heads/"+name)
	return sha, ok
}

// follow moves an open pull request's head to where its branch is now.
func (s *Server) follow(kept *repo, p *pull) {
	if p.state != open {
		return
	}
	if sha, ok := s.branch(kept, p.head); ok && sha != p.headSHA {
		p.headSHA = sha
		_, _ = s.gitIn(kept.owner, kept.name, "update-ref", "refs/pull/"+strconv.Itoa(p.number)+"/head", sha)
	}
}

// fork returns where the pull request's head left its base: the commit its
// files and commits are counted from.
func (s *Server) fork(kept *repo, p *pull) string {
	if p.forkPoint != "" {
		return p.forkPoint
	}
	base, _ := s.branch(kept, p.base)
	mb, err := s.gitIn(kept.owner, kept.name, "merge-base", base, p.headSHA)
	if err != nil {
		return base
	}
	return mb
}

// mergeTree returns the tree of ours with theirs merged in, and false when
// they conflict.
func (s *Server) mergeTree(kept *repo, ours, theirs string) (string, bool) {
	tree, err := s.gitIn(kept.owner, kept.name, "merge-tree", "--write-tree", ours, theirs)
	return tree, err == nil
}

func (s *Server) pullJSON(kept *repo, p *pull) map[string]any {
	base, _ := s.branch(kept, p.base)
	fork := s.fork(kept, p)
	var mergeable any
	if p.state == open {
		_, clean := s.mergeTree(kept, base, p.headSHA)
		mergeable = clean
	}
	var mergeCommit any
	if p.merged {
		mergeCommit = p.mergeCommit
	}
	additions, deletions, files := 0, 0, 0
	if out, err := s.gitIn(kept.owner, kept.name, "diff", "--numstat", fork, p.headSHA); err == nil && out != "" {
		for line := range strings.SplitSeq(out, "\n") {
			fields := strings.Split(line, "\t")
			a, _ := strconv.Atoi(fields[0])
			d, _ := strconv.Atoi(fields[1])
			additions, deletions, files = additions+a, deletions+d, files+1
		}
	}
	commits := 0
	if out, err := s.gitIn(kept.owner, kept.name, "rev-list", "--count", fork+".."+p.headSHA); err == nil {
		commits, _ = strconv.Atoi(out)
	}
	return map[string]any{
		"number": p.number, "title": p.title, "body": p.body, "state": p.state, "merged": p.merged,
		"html_url":  s.srv.URL + "/" + kept.owner + "/" + kept.name + "/pull/" + strconv.Itoa(p.number),
		"mergeable": mergeable, "merge_commit_sha": mergeCommit,
		"user":      map[string]string{"login": p.author},
		"head":      map[string]string{"ref": p.head, "sha": p.headSHA},
		"base":      map[string]string{"ref": p.base, "sha": base},
		"additions": additions, "deletions": deletions, "changed_files": files, "commits": commits,
	}
}

func (s *Server) openPull(w http.ResponseWriter, r *http.Request, kept *repo) {
	var body struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" || body.Head == "" || body.Base == "" {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	// A head may come as owner:branch.
	if _, branch, ok := strings.Cut(body.Head, ":"); ok {
		body.Head = branch
	}
	invalid := func(text string) {
		reply(w, http.StatusUnprocessableEntity, struct {
			apiError
			Errors []apiError `json:"errors"`
		}{message("Validation Failed"), []apiError{message(text)}})
	}
	head, hasHead := s.branch(kept, body.Head)
	base, hasBase := s.branch(kept, body.Base)
	if !hasHead || !hasBase {
		invalid("head or base is not a branch of the repository")
		return
	}
	for _, p := range kept.pulls {
		if p.state == open && p.head == body.Head {
			invalid("A pull request already exists for " + kept.owner + ":" + body.Head + ".")
			return
		}
	}
	if out, err := s.gitIn(kept.owner, kept.name, "rev-list", "--count", base+".."+head); err != nil || out == "0" {
		invalid("No commits between " + body.Base + " and " + body.Head)
		return
	}
	// A pull request is numbered among the issues, and is none of their lists.
	s.nextID++
	i := &issue{id: s.nextID, number: len(kept.issues) + 1, title: body.Title, body: body.Body, state: open,
		labels: []string{pullRequestMark}}
	kept.issues = append(kept.issues, i)
	p := &pull{number: i.number, title: body.Title, body: body.Body, head: body.Head, base: body.Base, author: s.acting,
		state: open}
	kept.pulls[p.number] = p
	s.follow(kept, p)
	reply(w, http.StatusCreated, s.pullJSON(kept, p))
}

func (s *Server) listPulls(w http.ResponseWriter, r *http.Request, kept *repo) {
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		state = open
	}
	_, head, _ := strings.Cut(q.Get("head"), ":")
	all := []map[string]any{}
	for n := 1; n <= len(kept.issues); n++ {
		p := kept.pulls[n]
		if p == nil || (state != listAll && p.state != state) || (head != "" && p.head != head) ||
			(q.Get("base") != "" && p.base != q.Get("base")) {
			continue
		}
		s.follow(kept, p)
		all = append(all, s.pullJSON(kept, p))
	}
	reply(w, http.StatusOK, page(q, all))
}

func (s *Server) patchPull(w http.ResponseWriter, r *http.Request, kept *repo, p *pull) {
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	if body.State == closed && p.state == open {
		p.forkPoint = s.fork(kept, p)
		p.state = closed
		kept.issue(p.number).state = closed
	}
	reply(w, http.StatusOK, s.pullJSON(kept, p))
}

func (s *Server) pullFiles(w http.ResponseWriter, r *http.Request, kept *repo, p *pull) {
	type file struct {
		Filename string `json:"filename"`
		Status   string `json:"status"`
		Previous string `json:"previous_filename,omitempty"`
	}
	files := []file{}
	out, err := s.gitIn(kept.owner, kept.name, "diff", "--name-status", "-M", s.fork(kept, p), p.headSHA)
	if err != nil {
		reply(w, http.StatusInternalServerError, message(err.Error()))
		return
	}
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(line, "\t")
		switch {
		case len(fields) == 3 && strings.HasPrefix(fields[0], "R"):
			files = append(files, file{Filename: fields[2], Status: "renamed", Previous: fields[1]})
		case len(fields) == 2:
			status := map[string]string{"A": "added", "D": "removed"}[fields[0]]
			if status == "" {
				status = "modified"
			}
			files = append(files, file{Filename: fields[1], Status: status})
		}
	}
	reply(w, http.StatusOK, page(r.URL.Query(), files))
}

func (s *Server) pullCommits(w http.ResponseWriter, r *http.Request, kept *repo, p *pull) {
	type parent struct {
		SHA string `json:"sha"`
	}
	type listed struct {
		SHA    string `json:"sha"`
		Commit struct {
			Author struct {
				Email string `json:"email"`
			} `json:"author"`
		} `json:"commit"`
		Parents []parent `json:"parents"`
	}
	commits := []listed{}
	out, err := s.gitIn(kept.owner, kept.name, "log", "--reverse", "--format=%H%x09%ae%x09%P",
		s.fork(kept, p)+".."+p.headSHA)
	if err != nil {
		reply(w, http.StatusInternalServerError, message(err.Error()))
		return
	}
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			continue
		}
		c := listed{SHA: fields[0], Parents: []parent{}}
		c.Commit.Author.Email = fields[1]
		for sha := range strings.FieldsSeq(fields[2]) {
			c.Parents = append(c.Parents, parent{sha})
		}
		commits = append(commits, c)
	}
	reply(w, http.StatusOK, page(r.URL.Query(), commits))
}

func (s *Server) mergePull(w http.ResponseWriter, r *http.Request, kept *repo, p *pull) {
	var body struct {
		Title   string `json:"commit_title"`
		Message string `json:"commit_message"`
		SHA     string `json:"sha"`
		Method  string `json:"merge_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	if p.state != open {
		reply(w, http.StatusMethodNotAllowed, message("Pull Request is not mergeable"))
		return
	}
	if body.SHA != "" && body.SHA != p.headSHA {
		reply(w, http.StatusConflict, message("Head branch was modified. Review and try the merge again."))
		return
	}
	if body.Method != "squash" {
		reply(w, http.StatusMethodNotAllowed, message("The fake GitHub merges by squash only"))
		return
	}
	base, _ := s.branch(kept, p.base)
	tree, clean := s.mergeTree(kept, base, p.headSHA)
	if !clean {
		reply(w, http.StatusMethodNotAllowed, message("Pull Request is not mergeable"))
		return
	}
	if s.refused(w, kept, ruleUpdate, "refs/heads/"+p.base, true) {
		return
	}
	text := body.Title
	if text == "" {
		text = p.title
	}
	if body.Message != "" {
		text += "\n\n" + body.Message
	}
	fork := s.fork(kept, p)
	commit, err := s.gitIn(kept.owner, kept.name, "commit-tree", tree, "-p", base, "-m", text)
	if err == nil {
		_, err = s.gitIn(kept.owner, kept.name, "update-ref", "refs/heads/"+p.base, commit, base)
	}
	if err != nil {
		reply(w, http.StatusInternalServerError, message(err.Error()))
		return
	}
	p.state, p.merged, p.mergeCommit, p.forkPoint = closed, true, commit, fork
	kept.issue(p.number).state = closed
	reply(w, http.StatusOK, struct {
		SHA     string `json:"sha"`
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
	}{commit, true, "Pull Request successfully merged"})
}

func (s *Server) updatePull(w http.ResponseWriter, kept *repo, p *pull) {
	if p.state != open {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	base, _ := s.branch(kept, p.base)
	if _, err := s.gitIn(kept.owner, kept.name, "merge-base", "--is-ancestor", base, p.headSHA); err == nil {
		reply(w, http.StatusUnprocessableEntity, message("There are no new commits on the base branch."))
		return
	}
	tree, clean := s.mergeTree(kept, p.headSHA, base)
	if !clean {
		reply(w, http.StatusUnprocessableEntity, message("merge conflict between base and head"))
		return
	}
	commit, err := s.gitIn(kept.owner, kept.name, "commit-tree", tree, "-p", p.headSHA, "-p", base, "-m",
		"Merge branch '"+p.base+"' into "+p.head)
	if err == nil {
		_, err = s.gitIn(kept.owner, kept.name, "update-ref", "refs/heads/"+p.head, commit, p.headSHA)
	}
	if err != nil {
		reply(w, http.StatusInternalServerError, message(err.Error()))
		return
	}
	s.follow(kept, p)
	reply(w, http.StatusAccepted, message("Updating pull request branch."))
}

func (s *Server) reviewPull(w http.ResponseWriter, r *http.Request, p *pull) {
	var body struct {
		Commit string `json:"commit_id"`
		Event  string `json:"event"`
		Body   string `json:"body"`
	}
	state := map[string]string{approve: "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED", "COMMENT": "COMMENTED"}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || state[body.Event] == "" {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	invalid := func(text string) {
		reply(w, http.StatusUnprocessableEntity, struct {
			apiError
			Errors []string `json:"errors"`
		}{message("Unprocessable Entity"), []string{text}})
	}
	if body.Event != "COMMENT" && s.acting == p.author {
		invalid(map[string]string{approve: "Can not approve your own pull request",
			"REQUEST_CHANGES": "Can not request changes on your own pull request"}[body.Event])
		return
	}
	if body.Event != approve && body.Body == "" {
		invalid("Body is required")
		return
	}
	if body.Commit == "" {
		body.Commit = p.headSHA
	}
	s.nextID++
	rv := &pullReview{ID: s.nextID, State: state[body.Event], Commit: body.Commit, Body: body.Body}
	rv.User.Login = s.acting
	p.reviews = append(p.reviews, rv)
	reply(w, http.StatusOK, rv)
}

func (s *Server) compare(w http.ResponseWriter, kept *repo, base, head string) {
	b, okBase := s.resolve(kept, base)
	h, okHead := s.resolve(kept, head)
	if !okBase || !okHead {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	out, err := s.gitIn(kept.owner, kept.name, "rev-list", "--left-right", "--count", b+"..."+h)
	fields := strings.Fields(out)
	if err != nil || len(fields) != 2 {
		reply(w, http.StatusInternalServerError, message("rev-list: "+out))
		return
	}
	behind, _ := strconv.Atoi(fields[0])
	ahead, _ := strconv.Atoi(fields[1])
	reply(w, http.StatusOK, struct {
		AheadBy  int `json:"ahead_by"`
		BehindBy int `json:"behind_by"`
	}{ahead, behind})
}
