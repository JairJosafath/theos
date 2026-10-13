package model

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/mod/module"
)

// FactoryModulePrefix starts the module paths the factory's own apps read from
// the Git host's Go package registry; every other path is read through the
// public Go proxy.
const FactoryModulePrefix = "c4factory.local/"

// ModuleCheckEvery is how often the Go proxy is asked about a version it
// hasn't confirmed yet.
const ModuleCheckEvery = 5 * time.Minute

// ModuleRegistry is how uploading a release's Go module to the Git host's
// registry went.
type ModuleRegistry string

const (
	// RegistryUploaded means the registry has the version, uploaded now or before.
	RegistryUploaded ModuleRegistry = "Uploaded"
	// RegistryNoGoMod means the repository has no go.mod at its root at the tag.
	RegistryNoGoMod ModuleRegistry = "NoGoMod"
	// RegistryInvalid means Go can't take the module at that version.
	RegistryInvalid ModuleRegistry = "Invalid"
	// RegistryFailed means the Git host couldn't be reached; it is tried again.
	RegistryFailed ModuleRegistry = "Failed"
)

// ProxyAnswer is what the Go proxy said about a module version.
type ProxyAnswer string

const (
	// ProxyFound is a 200: go get finds the version.
	ProxyFound ProxyAnswer = "Found"
	// ProxyMissing is a 404 or 410: the proxy can't fetch the version.
	ProxyMissing ProxyAnswer = "Missing"
	// ProxyUnreachable is a network error or any other status.
	ProxyUnreachable ProxyAnswer = "Unreachable"
)

// ProxyAnswerOf reads the Go proxy's HTTP answer to a version's .info request:
// the answer and the proxy's own first line, for a message.
func ProxyAnswerOf(code int, body string) (ProxyAnswer, string) {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	if len(line) > 300 {
		line = line[:300] + "..."
	}
	switch code {
	case 200:
		return ProxyFound, ""
	case 404, 410:
		return ProxyMissing, line
	}
	if line == "" {
		return ProxyUnreachable, fmt.Sprintf("HTTP %d", code)
	}
	return ProxyUnreachable, fmt.Sprintf("HTTP %d: %s", code, line)
}

// ModuleProxyURL is the go command's request for a module version's .info at
// the Go proxy, with the path and version escaped as the proxy protocol says.
func ModuleProxyURL(proxy, path, version string) (string, error) {
	p, err := module.EscapePath(path)
	if err != nil {
		return "", err
	}
	v, err := module.EscapeVersion(version)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(proxy, "/") + "/" + p + "/@v/" + v + ".info", nil
}

// ModuleFacts is what the operator knows about a release's Go module.
type ModuleFacts struct {
	// Path is the module path go.mod declares; Version is the release's tag.
	Path, Version string
	// Registry is how the upload went; RegistryDetail is the error of a
	// Failed one and the reason of an Invalid one.
	Registry       ModuleRegistry
	RegistryDetail string

	// Repository is the Crew's GitHub target, owner/name, "" when it publishes
	// no source; Source is where its source goes there.
	Repository string
	Source     *PublishSource
	// SourceTag is the published source's tag, status.lastRelease.publish.source.tag.
	SourceTag string
	// Private is set when GitHub reports the target private.
	Private bool
	// PublishedReason and PublishedMessage are the Published condition's,
	// when it is False.
	PublishedReason, PublishedMessage string

	// Proxy is the Go proxy's URL, "" when none is asked.
	Proxy string
	// Answer and AnswerDetail are the proxy's last answer, Checked when it
	// was given; a zero Checked means it was never asked, whatever Answer says.
	Answer       ProxyAnswer
	AnswerDetail string
	Checked      time.Time
	Now          time.Time
}

// States of a release's Go module, status.lastRelease.goModule.state.
const (
	ModuleStatePublished    = "Published"
	ModuleStateNotGo        = "NotGo"
	ModuleStateInvalid      = "Invalid"
	ModuleStateFailed       = "Failed"
	ModuleStateWaiting      = "Waiting"
	ModuleStateNotFetchable = "NotFetchable"
)

// Reasons of the ModulePublished condition.
const (
	ModuleReasonPublished        = "ModulePublished"
	ModuleReasonNotGo            = "NotGo"
	ModuleReasonInvalid          = "InvalidModule"
	ModuleReasonGitFailure       = "GitHostUnavailable"
	ModuleReasonWaitsForRelease  = "WaitsForRelease"
	ModuleReasonNotFetchable     = "NotFetchable"
	ModuleReasonProxyUnavailable = "ProxyUnavailable"
)

// ModuleDecision is what to say about a release's Go module: its state, the
// ModulePublished condition's value, reason and message. Ask means the proxy
// is to be asked now; the rest is then unset, and the operator decides again
// with the answer.
type ModuleDecision struct {
	Ask       bool
	State     string
	Fetchable bool
	Reason    string
	Message   string
}

// DecideModule says whether go get <path>@<version> finds a release's Go
// module where its path is read. A c4factory.local/ path is read from the
// registry, so the upload is the publish. Any other path is read through the
// Go proxy: the Crew's own GitHub source folder waits for its source tag, then
// the proxy is asked, at most every ModuleCheckEvery until it finds the
// version; with no proxy, or a private target, the source tag is the answer,
// and the factory can't tell for any other path.
func DecideModule(f ModuleFacts) ModuleDecision {
	switch f.Registry {
	case RegistryFailed:
		return ModuleDecision{State: ModuleStateFailed, Reason: ModuleReasonGitFailure,
			Message: fmt.Sprintf("Could not publish the Go module of %s: %s", f.Version, f.RegistryDetail)}
	case RegistryInvalid:
		return ModuleDecision{State: ModuleStateInvalid, Reason: ModuleReasonInvalid,
			Message: fmt.Sprintf("Did not publish %s %s: %s", f.Path, f.Version, f.RegistryDetail)}
	case RegistryNoGoMod:
		return ModuleDecision{State: ModuleStateNotGo, Fetchable: true, Reason: ModuleReasonNotGo,
			Message: fmt.Sprintf("%s has no go.mod at the repository's root; no Go module to publish", f.Version)}
	}
	published := fmt.Sprintf("Published %s %s", f.Path, f.Version)
	if strings.HasPrefix(f.Path, FactoryModulePrefix) {
		return ModuleDecision{State: ModuleStatePublished, Fetchable: true, Reason: ModuleReasonPublished, Message: published}
	}
	uploaded := fmt.Sprintf("Uploaded %s %s to the registry, which go get reads only for %s paths", f.Path, f.Version,
		FactoryModulePrefix)
	own, tag := f.ownFolder()
	tagOut := own && f.SourceTag == tag
	if own && !tagOut {
		msg := fmt.Sprintf("%s; go get reads it from GitHub, where %s has no tag %s yet", uploaded, f.Repository, tag)
		if f.PublishedReason != "" {
			msg += fmt.Sprintf(": Published is %s, %s", f.PublishedReason, f.PublishedMessage)
		}
		return ModuleDecision{State: ModuleStateWaiting, Reason: ModuleReasonWaitsForRelease, Message: msg}
	}
	if f.Proxy == "" || f.Private {
		switch {
		case tagOut && f.Private:
			return ModuleDecision{State: ModuleStatePublished, Fetchable: true, Reason: ModuleReasonPublished,
				Message: fmt.Sprintf("%s; %s is private, so fetch it with GOPRIVATE=%s", published, f.Repository, f.Path)}
		case tagOut:
			return ModuleDecision{State: ModuleStatePublished, Fetchable: true, Reason: ModuleReasonPublished,
				Message: fmt.Sprintf("%s, tagged %s on GitHub; no Go proxy was asked", published, tag)}
		}
		why := "the factory asks no Go proxy"
		if f.Private {
			why = fmt.Sprintf("%s is private, so no public Go proxy reads it", f.Repository)
		}
		return ModuleDecision{State: ModuleStateNotFetchable, Reason: ModuleReasonNotFetchable,
			Message: fmt.Sprintf("%s; %s, so the factory can't tell whether go get finds it: %s",
				uploaded, why, f.needs())}
	}
	if f.Checked.IsZero() || (f.Answer != ProxyFound && f.Now.Sub(f.Checked) >= ModuleCheckEvery) {
		return ModuleDecision{Ask: true}
	}
	switch f.Answer {
	case ProxyFound:
		return ModuleDecision{State: ModuleStatePublished, Fetchable: true, Reason: ModuleReasonPublished,
			Message: fmt.Sprintf("%s; %s has it", published, f.Proxy)}
	case ProxyMissing:
		return ModuleDecision{State: ModuleStateNotFetchable, Reason: ModuleReasonNotFetchable,
			Message: fmt.Sprintf("%s; %s can't fetch it (%s): %s", uploaded, f.Proxy, f.AnswerDetail, f.needs())}
	}
	return ModuleDecision{State: ModuleStateWaiting, Reason: ModuleReasonProxyUnavailable,
		Message: fmt.Sprintf("%s; could not ask %s whether go get finds it, asked again later: %s",
			uploaded, f.Proxy, f.AnswerDetail)}
}

// ownFolder says whether the module path is the Crew's own GitHub source
// folder, and the source tag go get looks for there.
func (f ModuleFacts) ownFolder() (bool, string) {
	if f.Repository == "" || f.Source == nil {
		return false, ""
	}
	tag := SourceTag(*f.Source, f.Version)
	return GoInstallProblem(f.Repository, f.Source.Path, f.Path, tag) == "", tag
}

// needs says what go get lacks for a path the proxy can't fetch: the Crew's
// folder's tag, what GoInstallProblem finds for a path in the Crew's GitHub
// repository, or that nothing in the Crew publishes to the path's host.
func (f ModuleFacts) needs() string {
	if own, tag := f.ownFolder(); own {
		return fmt.Sprintf("go get looks for the tag %s in %s", tag, f.Repository)
	}
	if f.Repository != "" && f.Source != nil {
		repo := "github.com/" + f.Repository
		if len(f.Path) >= len(repo) && strings.EqualFold(f.Path[:len(repo)], repo) &&
			(len(f.Path) == len(repo) || f.Path[len(repo)] == '/') {
			return GoInstallProblem(f.Repository, f.Source.Path, f.Path, SourceTag(*f.Source, f.Version))
		}
	}
	host, _, _ := strings.Cut(f.Path, "/")
	return fmt.Sprintf("nothing in this Crew publishes to %s; push the tag there by hand", host)
}
