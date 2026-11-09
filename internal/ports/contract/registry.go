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

package contract

import (
	"context"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// RegistryEnv builds the Registries under test.
type RegistryEnv struct {
	// New returns the GitHost of a fresh owner that holds no repository,
	// where the suite makes the repositories, and the owner's Registry.
	New func(t *testing.T) (ports.GitHost, ports.Registry)
	// GoMod, when set, is told that the go.mod pushed to repo declares module
	// path, before the suite publishes it: a host that keeps what a go.mod
	// declares apart from the files pushed records it, and a host that
	// keeps modules after a test may remove this one when t ends.
	GoMod func(t *testing.T, repo ports.Repository, path string)
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// Registry states what the controllers rely on of every Registry's Go
// modules: a repository with no go.mod is an outcome, not an error, and a
// module is published once, a repeat publishing nothing.
func Registry(t *testing.T, env RegistryEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, registrySuite{ctx: ctx, env: env}, env.Cannot, []subtest[registrySuite]{
		{"has no Go module to publish without a go.mod", registrySuite.noGoMod},
		{"publishes a Go module once", registrySuite.publishesGoModule},
	})
}

// registrySuite holds what every subtest of Registry shares.
type registrySuite struct {
	ctx context.Context
	env RegistryEnv
}

// tagged is a fresh owner's Registry and the contract repository holding the
// files files gives of it, tagged v0.1.0.
func (s registrySuite) tagged(t *testing.T, files func(repo ports.Repository) scaffold.Tree) (ports.Registry,
	ports.Repository) {
	t.Helper()
	h, registry := s.env.New(t)
	repo := must(h.EnsureRepository(s.ctx, contractRepo, "the Registry contract suite"))(t)
	head := must(h.PushInitialCommit(s.ctx, repo, files(repo), "Scaffold\n", contractAuthor))(t)
	if err := h.CreateTag(s.ctx, repo, firstTag, head, "Release v0.1.0"); err != nil {
		t.Fatal(err)
	}
	return registry, repo
}

func (s registrySuite) noGoMod(t *testing.T) {
	registry, repo := s.tagged(t, func(ports.Repository) scaffold.Tree { return seedFiles })
	want := ports.GoModule{Outcome: ports.GoModuleNoGoMod, Version: firstTag}
	for range 2 {
		if got, err := registry.PublishGoModule(s.ctx, repo, firstTag); err != nil || got != want {
			t.Errorf("PublishGoModule() of a repository with no go.mod = %+v, %v; want %+v", got, err, want)
		}
	}
}

func (s registrySuite) publishesGoModule(t *testing.T) {
	path := func(repo ports.Repository) string { return "example.com/" + repo.Owner + "/" + repo.Name }
	registry, repo := s.tagged(t, func(repo ports.Repository) scaffold.Tree {
		return scaffold.Tree{
			"go.mod": {Data: []byte("module " + path(repo) + "\n\ngo 1.24\n")},
			"greet/greet.go": {Data: []byte(
				"package greet\n\n// Hello greets.\nfunc Hello() string { return \"hello\" }\n")},
		}
	})
	if s.env.GoMod != nil {
		s.env.GoMod(t, repo, path(repo))
	}
	for _, want := range []ports.GoModuleOutcome{ports.GoModulePublished, ports.GoModuleExists} {
		got, err := registry.PublishGoModule(s.ctx, repo, firstTag)
		if w := (ports.GoModule{Outcome: want, Path: path(repo), Version: firstTag}); err != nil || got != w {
			t.Errorf("PublishGoModule() = %+v, %v; want %+v", got, err, w)
		}
	}
}
