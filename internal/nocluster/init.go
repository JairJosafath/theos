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

package nocluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// CredentialsSecret is the Secret name init writes on the GitProvider. No
// cluster reads it; a cluster the model moves to holds the credential there.
const CredentialsSecret = "git-credentials"

// githubHost is the one host init knows the provider of from the address
// alone; any other it asks for (decision 11).
const githubHost = "github.com"

// chartDir is where the theos repository keeps the chart whose values list
// the catalog: its entries and Bundles.
const chartDir = "dist/chart"

// InitOptions are what init writes the model files from.
type InitOptions struct {
	// Project is the project's name, the namespace of each object.
	Project string
	// Remote is the project repository's remote address, such as origin's:
	// the Git host the GitProvider names, and its owner.
	Remote string
	// Provider is the host's provider when the person named it; "" asks,
	// unless the remote is on github.com.
	Provider string
	// Ask asks the person a question and returns the answer.
	Ask func(question string) (string, error)
	// TheosURL is the theos repository, whose templates the catalog entries
	// name, and Ref the release's tag they are pinned to.
	TheosURL string
	Ref      string
}

// CheckProject refuses a project name that is not its namespace's, a DNS
// label; init checks it before anything is read, fetched or asked.
func CheckProject(name string) error {
	if name == "" {
		return errors.New("a project needs a name")
	}
	if len(k8svalidation.IsDNS1123Label(name)) > 0 {
		return errors.New("a project's name is its namespace's, a DNS label: at most 63 lower-case " +
			"letters, digits and '-', starting and ending with a letter or digit")
	}
	return nil
}

// Init returns the model files of a new project, by path: the Project; the
// GitProvider of the remote's host, its provider always written, and the
// catalog of the chart at the release (each entry naming the theos
// repository at the release's tag, with its template.path); and the
// settings with their defaults spelled out. The files decode (Decode).
func Init(ctx context.Context, fetcher modelfacts.TemplateFetcher, opts InitOptions) (map[string][]byte, error) {
	if err := CheckProject(opts.Project); err != nil {
		return nil, err
	}
	gitURL, owner, provider, err := host(opts)
	if err != nil {
		return nil, err
	}
	chart, err := fetcher.FetchTemplate(ctx, opts.TheosURL, opts.Ref, chartDir, nil)
	if err != nil {
		return nil, fmt.Errorf("reading the catalog of %s at %s: %w", opts.TheosURL, opts.Ref, err)
	}
	values, ok := chart["values.yaml"]
	if !ok {
		return nil, fmt.Errorf("%s at %s has no %s/values.yaml", opts.TheosURL, opts.Ref, chartDir)
	}
	var chartValues struct {
		Agents  chartAgents
		Catalog struct {
			Entries []struct {
				Kind, Name, Template string
				Spec                 map[string]any
			}
			Bundles []struct {
				Name string
				Spec map[string]any
			}
		}
	}
	if err := yaml.Unmarshal(values.Data, &chartValues); err != nil {
		return nil, fmt.Errorf("%s/values.yaml: %w", chartDir, err)
	}

	gv := c4v1beta1.GroupVersion.String()
	object := func(kind, name string, spec map[string]any) map[string]any {
		return map[string]any{"apiVersion": gv, "kind": kind, "metadata": map[string]any{"name": name}, "spec": spec}
	}
	setup := []map[string]any{object("GitProvider", provider, map[string]any{"provider": provider, "url": gitURL,
		"owner": owner, "credentialsSecretRef": map[string]any{"name": CredentialsSecret}})}
	for _, e := range chartValues.Catalog.Entries {
		spec := maps.Clone(e.Spec)
		dir := e.Template
		if dir == "" {
			dir = e.Name
		}
		spec["template"] = map[string]any{"url": opts.TheosURL, "ref": opts.Ref, "path": "templates/" + dir}
		setup = append(setup, object(e.Kind, e.Name, spec))
	}
	for _, b := range chartValues.Catalog.Bundles {
		setup = append(setup, object("Bundle", b.Name, b.Spec))
	}
	agents, err := chartValues.Agents.objects(chart, opts.Ref)
	if err != nil {
		return nil, err
	}
	for _, a := range agents {
		setup = append(setup, object("Agent", a.name, a.spec))
	}
	story := []map[string]any{object("Project", opts.Project, map[string]any{"displayName": opts.Project})}
	settings, err := DecodeSettings(nil, opts.Project)
	if err != nil {
		return nil, err
	}

	files := map[string][]byte{}
	if files[ModelFile], err = documents("The story: the Project, and the SoftwareSystems, Containers, DataStores, "+
		"Relationships and People a plan writes.", story); err != nil {
		return nil, err
	}
	if files[SetupFile], err = documents("What a person makes and no plan writes: the GitProvider, the catalog "+
		"entries at the release "+opts.Ref+", the Bundles, and the Agents a Crew names.", setup); err != nil {
		return nil, err
	}
	files[SetupFile] = append(files[SetupFile], crewTemplate...)
	if files[SettingsFile], err = yaml.Marshal(settings); err != nil {
		return nil, err
	}
	if _, err := Decode(files[ModelFile], files[SetupFile], files[SettingsFile]); err != nil {
		return nil, fmt.Errorf("the model init writes does not decode: %w", err)
	}
	return files, nil
}

// The Agents init writes: the chart's implementer and reviewer on Claude
// Code, at the release's agent image, so the file moves to a cluster
// unchanged (docs/plans/lead-runtime.md, decision 6).
var initAgents = []string{agentImplementer, agentReviewer}

// The names of the Agents init writes, the chart's.
const (
	agentImplementer = "implementer"
	agentReviewer    = "reviewer"
)

// chartAgents is what init reads of the chart's agents value.
type chartAgents struct {
	Image struct {
		Repository, Tag string
	}
	Implementer, Reviewer chartAgent
}

// chartAgent is one of the chart's Agents.
type chartAgent struct {
	Description string
	Model       string
	MaxRounds   int `json:"maxRounds"`
}

type agentObject struct {
	name string
	spec map[string]any
}

// objects are the Agents init writes, from the chart's values. The image's
// tag is the chart's, else its Chart.yaml's appVersion, else the release's
// version, as the chart pulls it.
func (a chartAgents) objects(chart scaffold.Tree, ref string) ([]agentObject, error) {
	if a.Image.Repository == "" {
		return nil, fmt.Errorf("%s/values.yaml names no agents.image.repository", chartDir)
	}
	tag := a.Image.Tag
	if tag == "" {
		if f, ok := chart["Chart.yaml"]; ok {
			var c struct {
				AppVersion string `json:"appVersion"`
			}
			if err := yaml.Unmarshal(f.Data, &c); err != nil {
				return nil, fmt.Errorf("%s/Chart.yaml: %w", chartDir, err)
			}
			tag = c.AppVersion
		}
	}
	if tag == "" {
		tag = strings.TrimPrefix(ref, "v")
	}
	out := make([]agentObject, 0, len(initAgents))
	for _, role := range initAgents {
		agent := a.Implementer
		if role == agentReviewer {
			agent = a.Reviewer
		}
		spec := map[string]any{"image": a.Image.Repository + ":" + tag, "harness": model.HarnessClaudeCode}
		if agent.Description != "" {
			spec["description"] = agent.Description
		}
		if agent.Model != "" {
			spec["model"] = agent.Model
		}
		if agent.MaxRounds > 0 {
			spec["maxRounds"] = agent.MaxRounds
		}
		out = append(out, agentObject{name: role, spec: spec})
	}
	return out, nil
}

// crewTemplate ends the setup file init writes: the Crew that works a
// Container's issues, left as a comment, as the model has no Container yet
// and a Crew names one.
var crewTemplate = []byte(`---
# Once model/model.yaml has a Container, a Crew works its issues: uncomment
# this and name the Container. theos status says what of it this runtime
# does not honour.
# apiVersion: theos.factory.io/v1beta1
# kind: Crew
# metadata:
#   name: <container>
# spec:
#   containerRef:
#     name: <container>
#   implementer:
#     name: implementer
#   reviewer:
#     name: reviewer
#   parallel: 1
`)

// documents is objs as one YAML stream under a comment.
func documents(comment string, objs []map[string]any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("# " + comment + "\n")
	for _, obj := range objs {
		data, err := yaml.Marshal(obj)
		if err != nil {
			return nil, err
		}
		b.WriteString("---\n")
		b.Write(data)
	}
	return b.Bytes(), nil
}

// host is the GitProvider's url, owner and provider for the remote: the
// provider of github.com is read from the address, any other's is the one
// the person named or answers. It is never guessed.
func host(opts InitOptions) (gitURL, owner, provider string, err error) {
	scheme, hostname, owner, err := parseRemote(opts.Remote)
	if err != nil {
		return "", "", "", err
	}
	provider = opts.Provider
	switch {
	case hostname == githubHost && provider != "" && provider != "github":
		return "", "", "", fmt.Errorf("%s is GitHub's; provider %q is another host's", hostname, provider)
	case hostname == githubHost:
		provider = "github"
	case provider == "":
		if opts.Ask == nil {
			return "", "", "", fmt.Errorf("which provider %s is cannot be read from its address; name it", hostname)
		}
		answer, err := opts.Ask(fmt.Sprintf("Which provider is %s (%s)? ", hostname, strings.Join(providers(), ", ")))
		if err != nil {
			return "", "", "", fmt.Errorf("which provider %s is: %w", hostname, err)
		}
		provider = strings.ToLower(strings.TrimSpace(answer))
	}
	if _, err := model.HostCI(provider); err != nil {
		return "", "", "", fmt.Errorf("provider %q: %w; the providers are %s", provider, err,
			strings.Join(providers(), ", "))
	}
	return scheme + "://" + hostname, owner, provider, nil
}

// providers are the GitProvider providers the factory knows a CI system of.
func providers() []string { return model.Hosts() }

// parseRemote reads a Git remote, https://host/owner/repo.git or
// git@host:owner/repo.git, into the host's web scheme, its name and the
// repository's owner. An SSH remote's host is reached over https.
func parseRemote(remote string) (scheme, hostname, owner string, err error) {
	if remote == "" {
		return "", "", "", errors.New("no remote: the project repository's remote names the Git host")
	}
	if !strings.Contains(remote, "://") {
		if at, rest, ok := strings.Cut(remote, ":"); ok && strings.Contains(at, "@") {
			remote = "ssh://" + at + "/" + rest
		}
	}
	// The errors leave the remote out: its address may carry a credential.
	u, err := url.Parse(remote)
	if err != nil {
		return "", "", "", errors.New("the remote is no address theos reads")
	}
	owner, _, _ = strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
	if u.Host == "" || owner == "" {
		return "", "", "", errors.New("the remote names no host and owner")
	}
	switch u.Scheme {
	case schemeHTTP, schemeHTTPS:
		return u.Scheme, u.Host, owner, nil
	default: // ssh: the port is SSH's, not the web's
		return schemeHTTPS, u.Hostname(), owner, nil
	}
}
