package features

import (
	"encoding/json"
	"testing"
)

func TestC4Session(t *testing.T) {
	tree, err := C4Session()
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
	if meta.ID != "c4session" || meta.PostStartCommand != "c4session-start" {
		t.Errorf("feature = %+v", meta)
	}
	for _, opt := range []string{"binaryUrl", "version", "actor", "summary"} {
		if _, ok := meta.Options[opt]; !ok {
			t.Errorf("option %s missing", opt)
		}
	}
}

func TestScaffoldOptions(t *testing.T) {
	local, err := ScaffoldOptions(SessionFeatureLocal, "http://example.com/c4session-${arch}")
	if err != nil {
		t.Fatal(err)
	}
	settings, ok := local.Features["./c4session"].(map[string]any)
	if !ok || settings["binaryUrl"] != "http://example.com/c4session-${arch}" {
		t.Errorf("local features = %v", local.Features)
	}
	if _, ok := local.DevcontainerFiles["c4session/install.sh"]; !ok {
		t.Errorf("local files = %v", local.DevcontainerFiles.Paths())
	}

	none, _ := ScaffoldOptions(SessionFeatureNone, "")
	if len(none.Features) != 0 || len(none.DevcontainerFiles) != 0 {
		t.Errorf("none = %+v", none)
	}

	oci, _ := ScaffoldOptions("ghcr.io/example/features/c4session:0", "")
	if _, ok := oci.Features["ghcr.io/example/features/c4session:0"]; !ok || len(oci.DevcontainerFiles) != 0 {
		t.Errorf("oci = %+v", oci)
	}
}
