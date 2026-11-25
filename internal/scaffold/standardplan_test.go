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

package scaffold

import (
	"slices"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

func TestUpdateFileAfter(t *testing.T) {
	current := Tree{"Makefile": {Data: []byte("check:\n")}}
	for _, tc := range []struct {
		name string
		plan Update
		want string
	}{
		{"kept", Update{}, "check:\n"},
		{"written", Update{Files: Tree{"Makefile": {Data: []byte("check-light:\n")}}}, "check-light:\n"},
		{"left out", Update{LeftOut: []string{"Makefile"}}, "check:\n"},
		{"removed", Update{Removes: []string{"Makefile"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(tc.plan.FileAfter(current, "Makefile")); got != tc.want {
				t.Errorf("FileAfter = %q, want %q", got, tc.want)
			}
		})
	}
}

// #295: the plan the operator and theos update share holds a change of
// checks back when a file it writes is left out, and when the Makefile it
// leaves has no light check.
func TestStandardRendersPlan295(t *testing.T) {
	every := model.StandardVersion{Ref: "v1", Checks: model.ChecksEveryChange}
	before := model.StandardVersion{Ref: "v2", Checks: model.ChecksBeforeRelease}
	for _, tc := range []struct {
		name          string
		r             StandardRenders
		current       Tree
		checksLeftOut []string
		noLightCheck  bool
		files         []string
	}{
		{name: "a plain update", r: StandardRenders{From: every, To: model.StandardVersion{Ref: "v2"},
			Old: tree("Makefile", "check:\n"), Next: tree("Makefile", "check:\n\ttest\n")},
			current: tree("Makefile", "check:\n"), files: []string{"Makefile"}},
		{name: "the checks' file left out", r: StandardRenders{From: every, To: before,
			Old: tree("ci.yml", "a\nb\nc\n"), Next: tree("ci.yml", "a\nB\nc\n"), ChecksPaths: []string{"ci.yml"}},
			current: tree("ci.yml", "a\nmine\nc\n"), checksLeftOut: []string{"ci.yml"}},
		{name: "no light check", r: StandardRenders{From: every, To: before,
			Old: tree("Makefile", "check:\n"), Next: tree("Makefile", "check:\n\ttest\n")},
			current: tree("Makefile", "check:\n"), noLightCheck: true, files: []string{"Makefile"}},
		{name: "a light check", r: StandardRenders{From: every, To: before,
			Old: tree("Makefile", "check:\n"), Next: tree("Makefile", "check:\ncheck-light:\n")},
			current: tree("Makefile", "check:\n"), files: []string{"Makefile"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.r.Plan(tc.current, nil)
			if !slices.Equal(p.ChecksLeftOut, tc.checksLeftOut) || p.NoLightCheck != tc.noLightCheck ||
				!slices.Equal(p.Files.Paths(), tc.files) {
				t.Errorf("#295 plan: checks left out %v, no light check %v, files %v; want %v, %v, %v",
					p.ChecksLeftOut, p.NoLightCheck, p.Files.Paths(), tc.checksLeftOut, tc.noLightCheck, tc.files)
			}
		})
	}
}

// #295: the version a Standard's files are at reads back from the history
// as a cluster keeps it in status: the scaffold's ref, then each update's,
// from its commit, a squash list or a pull request's description.
func TestStandardAt295(t *testing.T) {
	in := Input{Project: "shop", SoftwareSystem: "core", Container: "orders-api", Standard: "python-standard"}
	scaffoldCommit := CommitMessage(in, []string{"python-fastapi@v1.6.0", "python-standard@v1.0.0"}, nil)
	update := func(from, to model.StandardVersion) string {
		return UpdateMessage(in, "python-standard", from, to, Update{})
	}
	v := func(ref, checks string) model.StandardVersion { return model.StandardVersion{Ref: ref, Checks: checks} }
	every, before := model.ChecksEveryChange, model.ChecksBeforeRelease
	for _, tc := range []struct {
		name     string
		messages []string
		want     model.StandardVersion
		found    bool
	}{
		{"the scaffold alone", []string{scaffoldCommit}, v("v1.0.0", every), true},
		{"a Task's merge after it", []string{scaffoldCommit, "Add orders (#3)\n\ntask/orders-api-3\n"},
			v("v1.0.0", every), true},
		{"an update's commit", []string{scaffoldCommit, update(v("v1.0.0", ""), v("v1.1.0", ""))},
			v("v1.1.0", every), true},
		{"two updates, the newest last", []string{scaffoldCommit, update(v("v1.0.0", ""), v("v1.1.0", "")),
			update(v("v1.1.0", ""), v("v1.2.0", before))}, v("v1.2.0", before), true},
		{"a squash list", []string{scaffoldCommit, "Update python-standard (#7)\n\n* " +
			update(v("v1.0.0", ""), v("v1.1.0", "")) + "\n* Keep our Makefile target\n"}, v("v1.1.0", every), true},
		{"a pull request's description", []string{scaffoldCommit, "Bring python-standard to v1.1.0 (#7)\n\n" +
			"The pull request's words.\n\n" + UpdateTrailers("python-standard", v("v1.0.0", every),
			v("v1.1.0", before))}, v("v1.1.0", before), true},
		{"another Standard's update", []string{scaffoldCommit, UpdateMessage(in, "go-standard", v("v1", ""),
			v("v2", ""), Update{})}, v("v1.0.0", every), true},
		{"a take moves nothing", []string{scaffoldCommit, TakeMessage(in, "python-standard", "v0.9.0",
			Take{Taken: []model.TakenFile{{Path: "Makefile"}}})}, v("v1.0.0", every), true},
		{"an adoption", []string{"Import\n", AdoptionMessage(in, "python-standard", v("v1.3.0", ""))},
			v("v1.3.0", every), true},
		{"not part of the scaffold", []string{CommitMessage(in, []string{"python-fastapi@v1.6.0"}, nil)},
			model.StandardVersion{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := StandardAt(tc.messages, "python-standard")
			if got != tc.want || found != tc.found {
				t.Errorf("#295 StandardAt = %+v, %v; want %+v, %v", got, found, tc.want, tc.found)
			}
		})
	}
}

// theos scaffold and status find the Container's scaffold commit in a
// history a merge has moved on, where the newest commit is a Task's merge
// or an update's: they read only the newest and refused the Container.
func TestScaffoldCommitAfterAMerge(t *testing.T) {
	in := Input{Project: "shop", SoftwareSystem: "core", Container: "orders-api", Standard: "python-standard"}
	scaffoldCommit := CommitMessage(in, []string{"python-fastapi@v1.6.0", "python-standard@v1.0.0"}, nil)
	other := CommitMessage(Input{Project: "shop", SoftwareSystem: "core", Container: "billing-api"},
		[]string{"python-fastapi@v1.6.0"}, nil)
	update := UpdateMessage(in, "python-standard", model.StandardVersion{Ref: "v1.0.0"},
		model.StandardVersion{Ref: "v1.1.0"}, Update{})
	for _, tc := range []struct {
		name     string
		messages []string
		found    bool
	}{
		{"the scaffold alone", []string{scaffoldCommit}, true},
		{"a Task's merge after it", []string{scaffoldCommit, "Add orders (#3)\n\ntask/orders-api-3\n"}, true},
		{"a merged branch's commit listed after it", []string{"Add orders\n", scaffoldCommit, "Merge (#3)\n"}, true},
		{"an update after it", []string{scaffoldCommit, update}, true},
		{"an update alone names no layers", []string{"Import\n", update}, false},
		{"another Container's scaffold", []string{other, "Add orders (#3)\n"}, false},
		{"none of ours", []string{"Initial commit\n"}, false},
		{"no commits", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info, found := ScaffoldCommit(tc.messages, "shop/orders-api")
			if found != tc.found || found && (info.Container != "shop/orders-api" || len(info.ScaffoldedFrom) != 2) {
				t.Errorf("ScaffoldCommit = %+v, %v; want found %v", info, found, tc.found)
			}
		})
	}
}
