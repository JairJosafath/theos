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
