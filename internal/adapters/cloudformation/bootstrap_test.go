package cloudformation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The embedded bootstrap template is at least as new as every stack the
// fixtures' assemblies require (#1608).
func TestBootstrapTemplateCoversFixtures(t *testing.T) {
	body, version, err := bootstrapTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if body == "" || !strings.Contains(body, "CdkBootstrapVersion") {
		t.Fatalf("embedded bootstrap template is empty or lacks CdkBootstrapVersion")
	}
	if !strings.Contains(body, "'"+strconv.Itoa(version)+"'") {
		t.Errorf("template body does not carry its version %d", version)
	}
	manifests, err := filepath.Glob("testdata/*/*manifest.json")
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no fixture manifests found: %v", err)
	}
	stacks := 0
	for _, path := range manifests {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Artifacts map[string]struct {
				Type       string `json:"type"`
				Properties struct {
					Required *int `json:"requiresBootstrapStackVersion"`
				} `json:"properties"`
			} `json:"artifacts"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for name, a := range m.Artifacts {
			if a.Type != "aws:cloudformation:stack" || a.Properties.Required == nil {
				continue
			}
			stacks++
			if *a.Properties.Required > version {
				t.Errorf("%s: %s requires bootstrap version %d, embedded template is %d", path, name, *a.Properties.Required, version)
			}
		}
	}
	if stacks == 0 {
		t.Fatal("no stack artifact with requiresBootstrapStackVersion in the fixtures")
	}
}
