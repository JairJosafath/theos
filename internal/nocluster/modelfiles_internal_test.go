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
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
)

// #268: a whole number in a model file reaches the CRD's rules as an
// integer, as on the API server: a CEL rule on an integer field holds for a
// value within it and refuses one beyond it.
func TestIntegersReachTheRulesAsIntegers268(t *testing.T) {
	schema := &apiextensions.JSONSchemaProps{Type: "object", Properties: map[string]apiextensions.JSONSchemaProps{
		"spec": {Type: "object", Properties: map[string]apiextensions.JSONSchemaProps{
			"size": {Type: "integer"},
		}, XValidations: apiextensions.ValidationRules{{Rule: "self.size <= 8", Message: "size is at most 8"}}},
	}}
	s, err := newCRDSchema("Scratch", schema, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		doc     string
		refused bool
	}{
		{"spec:\n  size: 3\n", false},
		{"spec:\n  size: 9\n", true},
	} {
		obj, err := objectOf([]byte(c.doc))
		if err != nil {
			t.Fatal(err)
		}
		if size := obj["spec"].(map[string]any)["size"]; size != int64(3) && size != int64(9) {
			t.Errorf("#268 %q: size is %T %v, want an int64", c.doc, size, size)
		}
		err = s.create(obj)
		switch {
		case c.refused && (err == nil || !strings.Contains(err.Error(), "size is at most 8")):
			t.Errorf("#268 %q: err = %v, want the rule's message", c.doc, err)
		case !c.refused && err != nil:
			t.Errorf("#268 %q: %v", c.doc, err)
		}
	}
}
