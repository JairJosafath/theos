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

package features

import (
	"encoding/json"
	"testing"
)

func TestTheosSession(t *testing.T) {
	tree, err := TheosSession()
	if err != nil {
		t.Fatal(err)
	}
	if !tree["install.sh"].Executable {
		t.Error("install.sh is not executable")
	}
	var meta struct {
		ID               string         `json:"id"`
		Options          map[string]any `json:"options"`
		PostStartCommand string         `json:"postStartCommand"`
	}
	if err := json.Unmarshal(tree["devcontainer-feature.json"].Data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.ID != "theos-session" || meta.PostStartCommand != "theos-session-start" {
		t.Errorf("feature = %+v", meta)
	}
	for _, opt := range []string{"binaryUrl", "version", "actor", "summary", "signerIdentity", "signerIssuer"} {
		if _, ok := meta.Options[opt]; !ok {
			t.Errorf("option %s missing", opt)
		}
	}
}

func TestScaffoldOptions(t *testing.T) {
	local, err := ScaffoldOptions(SessionFeatureLocal, "http://example.com/theos-session-${arch}")
	if err != nil {
		t.Fatal(err)
	}
	settings, ok := local.Features["./theos-session"].(map[string]any)
	if !ok || settings["binaryUrl"] != "http://example.com/theos-session-${arch}" {
		t.Errorf("local features = %v", local.Features)
	}
	if _, ok := local.DevcontainerFiles["theos-session/install.sh"]; !ok {
		t.Errorf("local files = %v", local.DevcontainerFiles.Paths())
	}

	none, _ := ScaffoldOptions(SessionFeatureNone, "")
	if len(none.Features) != 0 || len(none.DevcontainerFiles) != 0 {
		t.Errorf("none = %+v", none)
	}

	oci, _ := ScaffoldOptions("ghcr.io/example/features/theos-session:0", "")
	if _, ok := oci.Features["ghcr.io/example/features/theos-session:0"]; !ok || len(oci.DevcontainerFiles) != 0 {
		t.Errorf("oci = %+v", oci)
	}
}
