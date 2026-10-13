package model

import (
	"slices"
	"testing"
)

// The function type lands before its templates (plans/aws.md, step 1): a
// Container may be one, but no catalog entry in the chart supports it, so
// nothing can pick it until the aws-lambda-ts step adds one.
func TestFunctionTypeIsKnownButUnsupportedByTheChart(t *testing.T) {
	if !slices.Contains(containerTypes, TypeFunction) {
		t.Errorf("containerTypes = %v, want it to hold %q", containerTypes, TypeFunction)
	}
	entries := chartCatalog(t)
	if len(entries) == 0 {
		t.Fatalf("%s has no catalog entries", chartValues)
	}
	for _, e := range entries {
		if slices.Contains(e.SupportedTypes, TypeFunction) {
			t.Errorf("%s %s supports %q, before its step adds it", e.Side, e.Name, TypeFunction)
		}
	}
}
