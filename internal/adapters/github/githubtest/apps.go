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
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The fake's GitHub Apps, and what a token may do, step 4 of
// docs/plans/github-host.md (#77). Each call answers as GitHub documents it:
//
//	GET    /repos/{o}/{r}/installation             the App's installation on a repository's account; 404 without one
//	GET    /orgs/{org}/installation                the same, by organization
//	GET    /users/{user}/installation              the same, by user
//	POST   /app/installations/{id}/access_tokens   mint an installation token for repositories and permissions; 422 for a
//	                                               repository the installation doesn't reach, or a permission the App lacks
//	DELETE /installation/token                     revoke the token the call is made with
//
// https://docs.github.com/en/rest/apps/apps and /rest/apps/installations.
// The first four take the App's JSON Web Token, RS256 over its private key,
// at most ten minutes long; the last the installation token itself.
//
// A token of an installation is the App, "<slug>[bot]", not a person. It
// reaches the repositories it was minted for and no other, which answer 404,
// with the permissions it was minted with and no other, which answer 403
// "Resource not accessible by integration". A person's token does what its
// account may: New's user administers, the others write.
//
// A ruleset holds whoever it doesn't let bypass it, whatever their token may
// write: a push meets it in each repository's pre-receive hook, which the
// fake tells the refs this push may not touch, and a merge or a ref written
// over REST in the handler. GitHub answers a push with "GH013: Repository
// rule violations found", https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets.
// A push that changes a file under .github/workflows/ needs the workflows
// permission, which GitHub refuses with "refusing to allow a GitHub App to
// create or update workflow ... without `workflows` permission".

// app is a registered GitHub App: its public key, the permissions it was
// given, and the accounts it is installed on with the installation's id.
type app struct {
	id       int64
	slug     string
	key      *rsa.PublicKey
	perms    map[string]string
	installs map[string]int64
}

// grant is what an installation token may do: the App it is of, the
// account, the repositories it reaches and its permissions.
type grant struct {
	app   *app
	owner string
	repos []string
	perms map[string]string
}

// The levels of a permission, and the permissions the fake tells apart.
const (
	levelRead  = "read"
	levelWrite = "write"

	permActions        = "actions"
	permAdministration = "administration"
	permChecks         = "checks"
	permContents       = "contents"
	permHooks          = "repository_hooks"
	permIssues         = "issues"
	permPulls          = "pull_requests"
	permStatuses       = "statuses"
	permVariables      = "actions_variables"
	permWorkflows      = "workflows"
)

// allows says whether the grant holds perm, to write when write.
func (g *grant) allows(perm string, write bool) bool {
	level := g.perms[perm]
	return level == levelWrite || !write && level == levelRead
}

// AddApp registers a GitHub App of slug with its public key and the
// permissions it was given ("contents": "write", ...), installs it on
// account, an organization or a user, for all of its repositories, and
// returns the App's id and the installation's.
func (s *Server) AddApp(slug, account string, key *rsa.PublicKey, permissions map[string]string) (appID, installationID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID += 2
	a := &app{id: s.nextID - 1, slug: slug, key: key, perms: permissions, installs: map[string]int64{account: s.nextID}}
	s.apps = append(s.apps, a)
	return a.id, s.nextID
}

// Tokens returns how many installation tokens of the App of appID are live.
func (s *Server) Tokens(appID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, g := range s.grants {
		if g.app.id == appID {
			n++
		}
	}
	return n
}

var (
	installationPath = regexp.MustCompile(`^/api/v3/(?:repos/([^/]+)/[^/]+|orgs/([^/]+)|users/([^/]+))/installation$`)
	accessTokensPath = regexp.MustCompile(`^/api/v3/app/installations/(\d+)/access_tokens$`)
)

const revokePath = "/api/v3/installation/token"

// appsAPI serves the calls an App makes as itself, and says whether r was
// one.
func (s *Server) appsAPI(w http.ResponseWriter, r *http.Request) bool {
	lookup, mint := installationPath.FindStringSubmatch(r.URL.Path), accessTokensPath.FindStringSubmatch(r.URL.Path)
	if (lookup == nil || r.Method != http.MethodGet) && (mint == nil || r.Method != http.MethodPost) {
		return false
	}
	a := s.appOf(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if a == nil {
		reply(w, http.StatusUnauthorized, message("A JSON web token could not be decoded"))
		return true
	}
	if lookup != nil {
		account := lookup[1] + lookup[2] + lookup[3]
		id, installed := a.installs[account]
		if !installed {
			reply(w, http.StatusNotFound, message("Not Found"))
			return true
		}
		reply(w, http.StatusOK, map[string]any{"id": id, "app_id": a.id, "app_slug": a.slug,
			"account": map[string]string{"login": account}})
		return true
	}
	s.mintToken(w, r, a, mint[1])
	return true
}

// appOf returns the App a JSON Web Token is of: signed with its key, issued
// by its id, not yet expired and at most ten minutes long.
func (s *Server) appOf(jwt string) *app {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil
	}
	var claims struct {
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		Issuer    string `json:"iss"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	now := time.Now().Unix()
	if claims.ExpiresAt <= now || claims.IssuedAt > now || claims.ExpiresAt-claims.IssuedAt > 600 {
		return nil
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	for _, a := range s.apps {
		if strconv.FormatInt(a.id, 10) == claims.Issuer &&
			rsa.VerifyPKCS1v15(a.key, crypto.SHA256, digest[:], sig) == nil {
			return a
		}
	}
	return nil
}

func (s *Server) mintToken(w http.ResponseWriter, r *http.Request, a *app, installation string) {
	id, _ := strconv.ParseInt(installation, 10, 64)
	owner := ""
	for account, installed := range a.installs {
		if installed == id {
			owner = account
		}
	}
	if owner == "" {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	var body struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	g := &grant{app: a, owner: owner, repos: body.Repositories, perms: body.Permissions}
	if len(g.repos) == 0 {
		// Every repository of the account, as an installation on all of them reaches.
		for _, kept := range s.repos {
			if kept.owner == owner {
				g.repos = append(g.repos, kept.name)
			}
		}
	}
	for _, name := range body.Repositories {
		if _, ok := s.repos[owner+"/"+name]; !ok {
			reply(w, http.StatusUnprocessableEntity, message("There is at least one repository that does not exist or "+
				"is not accessible to the parent installation."))
			return
		}
	}
	if g.perms == nil {
		g.perms = a.perms
	}
	for perm, level := range g.perms {
		if has := a.perms[perm]; has != levelWrite && has != level {
			reply(w, http.StatusUnprocessableEntity, message("The permissions requested are not granted to this "+
				"installation."))
			return
		}
	}
	random := make([]byte, 20)
	_, _ = rand.Read(random)
	token := "ghs_" + hex.EncodeToString(random)
	s.tokens[token], s.grants[token] = a.slug+"[bot]", g
	// A token asked for with repositories says which; one of the whole
	// installation says "all", as an installation on every repository does.
	answer := map[string]any{"token": token, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"permissions": g.perms, "repository_selection": listAll}
	if len(body.Repositories) > 0 {
		named := make([]map[string]string, 0, len(body.Repositories))
		for _, name := range body.Repositories {
			named = append(named, map[string]string{"name": name, "full_name": owner + "/" + name})
		}
		answer["repository_selection"], answer["repositories"] = "selected", named
	}
	reply(w, http.StatusCreated, answer)
}

// revokeToken serves DELETE /installation/token: the installation token the
// call is made with is no token any more.
func (s *Server) revokeToken(w http.ResponseWriter, token string) {
	if s.grants[token] == nil {
		reply(w, http.StatusForbidden, message("Resource not accessible by personal access token"))
		return
	}
	delete(s.tokens, token)
	delete(s.grants, token)
	reply(w, http.StatusNoContent, nil)
}

var (
	variablesAt = regexp.MustCompile(`^/actions/variables(/|$)`)
	rulesetsAt  = regexp.MustCompile(`^/rulesets(/|$)`)
	pullsAt     = regexp.MustCompile(`^/pulls(/|$)`)
	issuesAt    = regexp.MustCompile(`^/(issues|labels|milestones)(/|$)`)
	contentsOfA = regexp.MustCompile(`^/pulls/\d+/(merge|update-branch)$`)
)

// needs is the permission a call on a repository takes of an installation
// token, and whether it writes. A repository's own description takes none
// a token lacks: metadata, which every installation token reads.
func needs(method, rest string) (perm string, write bool) {
	write = method != http.MethodGet
	switch {
	case rest == "":
		return "", write
	case hooksAt.MatchString(rest):
		return permHooks, write
	case actionsAt.MatchString(rest):
		return permActions, write
	case variablesAt.MatchString(rest):
		return permVariables, write
	case rulesetsAt.MatchString(rest):
		return permAdministration, write
	case contentsOfA.MatchString(rest):
		return permContents, write
	case pullsAt.MatchString(rest):
		return permPulls, write
	case issuesAt.MatchString(rest):
		return permIssues, write
	case checkRunsPath.MatchString(rest):
		return permChecks, write
	case combinedPath.MatchString(rest), statusesPath.MatchString(rest), setStatusPath.MatchString(rest):
		return permStatuses, write
	}
	return permContents, write
}

// permitted answers 404 or 403 for a call on owner/name an installation
// token may not make, and says whether the call may go on. A person's token
// is held by its handler alone.
func (s *Server) permitted(w http.ResponseWriter, r *http.Request, owner, name, rest string) bool {
	g := s.grant
	if g == nil {
		return true
	}
	if g.owner != owner || !slices.Contains(g.repos, name) {
		reply(w, http.StatusNotFound, message("Not Found"))
		return false
	}
	if perm, write := needs(r.Method, rest); perm != "" && !g.allows(perm, write) {
		reply(w, http.StatusForbidden, message("Resource not accessible by integration"))
		return false
	}
	return true
}

// The rules of a ruleset the fake holds a ref's writer to, the enforcement
// of one that holds anybody, the ways past one, and the id of the
// repository role admin among a ruleset's bypass actors.
const (
	ruleCreation       = "creation"
	ruleUpdate         = "update"
	ruleDeletion       = "deletion"
	ruleNonFastForward = "non_fast_forward"
	enforced           = "active"
	bypassAlways       = "always"
	bypassByPull       = "pull_request"
	adminRole          = 5
)

// bypasses says whether the caller, login with its grant, is let past a
// ruleset: always, or when merging a pull request.
func (s *Server) bypasses(rs *Ruleset, login string, g *grant, viaPull bool) bool {
	for _, actor := range rs.BypassActors {
		is := actor.ActorType == "RepositoryRole" && actor.ActorID == adminRole && g == nil && login == s.admin ||
			actor.ActorType == "Integration" && g != nil && actor.ActorID == g.app.id
		if is && (actor.BypassMode == bypassAlways || viaPull && actor.BypassMode == bypassByPull) {
			return true
		}
	}
	return false
}

// matches says whether a ruleset's conditions take in ref.
func matches(rs *Ruleset, ref string) bool {
	in := func(patterns []string) bool {
		for _, pattern := range patterns {
			switch pattern {
			case "~ALL":
				return true
			case "~DEFAULT_BRANCH":
				pattern = "refs/heads/main"
			}
			if ok, _ := path.Match(pattern, ref); ok {
				return true
			}
		}
		return false
	}
	return in(rs.Conditions.RefName.Include) && !in(rs.Conditions.RefName.Exclude)
}

// held lists what the active rulesets of a repository hold the caller to
// outside a pull request: a "<rule> <ref pattern>" line for each rule it
// doesn't bypass, as the pre-receive hook reads them.
func (s *Server) held(kept *repo, login string, g *grant) string {
	var lines []string
	for _, rs := range kept.rulesets {
		if rs.Enforcement != enforced || s.bypasses(rs, login, g, false) {
			continue
		}
		for _, rule := range rs.Rules {
			for _, pattern := range rs.Conditions.RefName.Include {
				lines = append(lines, rule.Type+" "+pattern)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// refused answers 422 with GitHub's words when a ruleset holds the caller
// of this REST call from doing rule (ruleCreation, ruleUpdate or
// ruleDeletion) to ref, and says whether it did. viaPull is a pull
// request's merge.
func (s *Server) refused(w http.ResponseWriter, kept *repo, rule, ref string, viaPull bool) bool {
	for _, rs := range kept.rulesets {
		if rs.Enforcement != enforced || !matches(rs, ref) || s.bypasses(rs, s.acting, s.grant, viaPull) ||
			!slices.ContainsFunc(rs.Rules, func(r struct {
				Type string `json:"type"`
			}) bool {
				return r.Type == rule
			}) {
			continue
		}
		status := http.StatusUnprocessableEntity
		if viaPull {
			// A merge the rules refuse is "not allowed", as one that conflicts is.
			status = http.StatusMethodNotAllowed
		}
		reply(w, status, message("Repository rule violations found\n\nCannot "+rule+" this protected ref."))
		return true
	}
	return false
}

// preReceive is the hook of every repository of the fake: it refuses the
// refs of a push that GITHUB_FAKE_HELD names, one "<rule> <ref pattern>" a
// line, and, for a pusher GITHUB_FAKE_WORKFLOWS says no to, a change under
// .github/workflows/.
const preReceive = `#!/bin/sh
set -f
zero=0000000000000000000000000000000000000000
fields=$IFS
lines='
'
tick=$(printf '\140')
denied=0
while read -r old new ref; do
	op=update
	[ "$old" = "$zero" ] && op=creation
	[ "$new" = "$zero" ] && op=deletion
	IFS=$lines
	for line in $GITHUB_FAKE_HELD; do
		rule=${line%% *}
		pattern=${line#* }
		case "$ref" in
		$pattern) ;;
		*) continue ;;
		esac
		if [ "$rule" = non_fast_forward ]; then
			[ "$op" = update ] || continue
			git merge-base --is-ancestor "$old" "$new" && continue
		elif [ "$rule" != "$op" ]; then
			continue
		fi
		echo "error: GH013: Repository rule violations found for $ref." >&2
		echo "- Cannot $rule this protected ref." >&2
		denied=1
	done
	IFS=$fields
	if [ "$GITHUB_FAKE_WORKFLOWS" = no ] && [ "$op" != deletion ]; then
		base=$old
		[ "$op" = creation ] && base=$(git merge-base "$new" HEAD 2>/dev/null)
		if [ -n "$base" ]; then
			changed=$(git diff --name-only "$base" "$new" -- .github/workflows | head -n 1)
		else
			changed=$(git ls-tree -r --name-only "$new" -- .github/workflows | head -n 1)
		fi
		if [ -n "$changed" ]; then
			echo "error: refusing to allow a GitHub App to create or update workflow $tick$changed$tick without ${tick}workflows$tick permission" >&2
			denied=1
		fi
	fi
done
exit $denied
`
