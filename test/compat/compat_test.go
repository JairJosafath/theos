/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

// Package compat guards the v1alpha1 compatibility promise (docs/design.md,
// "Compatibility"): an object a client stored under the last release still
// validates after an upgrade. baseline/ is config/crd/bases as of the last
// release; hack/release.sh copies it there as it tags.
package compat

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// schema is the part of an OpenAPI schema the promise is about.
type schema struct {
	Type                 string             `json:"type"`
	IntOrString          bool               `json:"x-kubernetes-int-or-string"`
	Properties           map[string]*schema `json:"properties"`
	Items                *schema            `json:"items"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	Required             []string           `json:"required"`
	Enum                 []json.RawMessage  `json:"enum"`
	Default              json.RawMessage    `json:"default"`
	Nullable             bool               `json:"nullable"`
	PreserveUnknown      bool               `json:"x-kubernetes-preserve-unknown-fields"`

	MaxLength     *float64 `json:"maxLength"`
	MaxItems      *float64 `json:"maxItems"`
	MaxProperties *float64 `json:"maxProperties"`
	Maximum       *float64 `json:"maximum"`
	MinLength     *float64 `json:"minLength"`
	MinItems      *float64 `json:"minItems"`
	MinProperties *float64 `json:"minProperties"`
	Minimum       *float64 `json:"minimum"`

	ExclusiveMaximum bool `json:"exclusiveMaximum"`
	ExclusiveMinimum bool `json:"exclusiveMinimum"`

	Pattern     string   `json:"pattern"`
	Format      string   `json:"format"`
	ListType    string   `json:"x-kubernetes-list-type"`
	ListMapKeys []string `json:"x-kubernetes-list-map-keys"`
	MapType     string   `json:"x-kubernetes-map-type"`
}

// crdVersion is one version of a CRD.
type crdVersion struct {
	Name   string `json:"name"`
	Served bool   `json:"served"`
	Schema struct {
		OpenAPIV3Schema *schema `json:"openAPIV3Schema"`
	} `json:"schema"`
}

// crd is the part of a CustomResourceDefinition the promise is about.
type crd struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Versions []crdVersion `json:"versions"`
	} `json:"spec"`
}

// typeOf names a schema's type, int-or-string included.
func typeOf(s *schema) string {
	if s.IntOrString {
		return "int-or-string"
	}
	return s.Type
}

// widens says whether a value of type from is always a value of type to.
func widens(from, to string) bool {
	return from == to || (to == "int-or-string" && (from == "integer" || from == "string"))
}

// sameJSON says whether two JSON values are equal, however they are spelled.
func sameJSON(a, b json.RawMessage) bool {
	var av, bv any
	return json.Unmarshal(a, &av) == nil && json.Unmarshal(b, &bv) == nil && reflect.DeepEqual(av, bv)
}

// compareBounds adds to breaks every bound cur tightens: a maximum lowered or
// added, a minimum raised or added, a bound made exclusive, a pattern, format
// or list or map type changed, a default changed or removed (#1188), null or
// unknown fields no longer kept. A default added where the base had none is
// left to review: the schema can't say whether the operator already read an
// unset field that way.
func compareBounds(path string, base, cur *schema, breaks *[]string) {
	upper := func(name string, b, c *float64) {
		if c != nil && (b == nil || *c < *b) {
			*breaks = append(*breaks, fmt.Sprintf("%s: %s tightened", path, name))
		}
	}
	lower := func(name string, b, c *float64) {
		if c != nil && (b == nil || *c > *b) {
			*breaks = append(*breaks, fmt.Sprintf("%s: %s tightened", path, name))
		}
	}
	same := func(name, b, c string) {
		if b != c {
			*breaks = append(*breaks, fmt.Sprintf("%s: %s %q became %q", path, name, b, c))
		}
	}
	kept := func(name string, b, c bool) {
		if b && !c {
			*breaks = append(*breaks, fmt.Sprintf("%s: %s true became false", path, name))
		}
	}
	upper("maxLength", base.MaxLength, cur.MaxLength)
	upper("maxItems", base.MaxItems, cur.MaxItems)
	upper("maxProperties", base.MaxProperties, cur.MaxProperties)
	upper("maximum", base.Maximum, cur.Maximum)
	lower("minLength", base.MinLength, cur.MinLength)
	lower("minItems", base.MinItems, cur.MinItems)
	lower("minProperties", base.MinProperties, cur.MinProperties)
	lower("minimum", base.Minimum, cur.Minimum)
	exclusive := func(name string, b, c bool) {
		if c && !b {
			*breaks = append(*breaks, fmt.Sprintf("%s: %s tightened", path, name))
		}
	}
	exclusive("exclusiveMaximum", base.ExclusiveMaximum, cur.ExclusiveMaximum)
	exclusive("exclusiveMinimum", base.ExclusiveMinimum, cur.ExclusiveMinimum)
	same("pattern", base.Pattern, cur.Pattern)
	same("format", base.Format, cur.Format)
	same("x-kubernetes-list-type", base.ListType, cur.ListType)
	same("x-kubernetes-list-map-keys", strings.Join(base.ListMapKeys, ","), strings.Join(cur.ListMapKeys, ","))
	same("x-kubernetes-map-type", base.MapType, cur.MapType)
	kept("nullable", base.Nullable, cur.Nullable)
	kept("x-kubernetes-preserve-unknown-fields", base.PreserveUnknown, cur.PreserveUnknown)
	switch {
	case len(base.Default) == 0:
	case len(cur.Default) == 0:
		*breaks = append(*breaks, fmt.Sprintf("%s: default %s removed", path, base.Default))
	case !sameJSON(base.Default, cur.Default):
		*breaks = append(*breaks, fmt.Sprintf("%s: default %s became %s", path, base.Default, cur.Default))
	}
}

// mapValues is the schema of a map's values, or nil when it has none.
func mapValues(s *schema) *schema {
	var v schema
	if len(s.AdditionalProperties) == 0 || json.Unmarshal(s.AdditionalProperties, &v) != nil {
		return nil
	}
	return &v
}

// compareSchema adds to breaks every way cur refuses, or reads differently,
// an object base accepted at path: a lost path, a changed type, a new enum or
// a lost enum value, a tightened bound or default, a field required under an
// object the base already had.
func compareSchema(path string, base, cur *schema, breaks *[]string) {
	if cur == nil {
		*breaks = append(*breaks, path+": removed")
		return
	}
	if !widens(typeOf(base), typeOf(cur)) {
		*breaks = append(*breaks, fmt.Sprintf("%s: type %q became %q", path, typeOf(base), typeOf(cur)))
		return
	}
	compareBounds(path, base, cur, breaks)
	if len(base.Enum) == 0 && len(cur.Enum) > 0 {
		*breaks = append(*breaks, path+": enum added")
	}
	if len(cur.Enum) > 0 {
		for _, v := range base.Enum {
			if !slices.ContainsFunc(cur.Enum, func(c json.RawMessage) bool { return string(c) == string(v) }) {
				*breaks = append(*breaks, fmt.Sprintf("%s: enum value %s removed", path, v))
			}
		}
	}
	for _, r := range cur.Required {
		if !slices.Contains(base.Required, r) {
			*breaks = append(*breaks, fmt.Sprintf("%s.%s: newly required", path, r))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(base.Properties)) {
		compareSchema(path+"."+name, base.Properties[name], cur.Properties[name], breaks)
	}
	if base.Items != nil {
		compareSchema(path+"[]", base.Items, cur.Items, breaks)
	}
	if bv := mapValues(base); bv != nil {
		compareSchema(path+"{}", bv, mapValues(cur), breaks)
	}
}

// compareCRDs lists every break between the base CRDs and the current ones,
// by CRD name: a CRD or served version gone, or a schema change compareSchema
// finds.
func compareCRDs(base, cur map[string]crd) []string {
	var breaks []string
	for _, name := range slices.Sorted(maps.Keys(base)) {
		c, ok := cur[name]
		if !ok {
			breaks = append(breaks, name+": removed")
			continue
		}
		for _, bv := range base[name].Spec.Versions {
			if !bv.Served {
				continue
			}
			i := slices.IndexFunc(c.Spec.Versions, func(v crdVersion) bool {
				return v.Name == bv.Name && v.Served
			})
			if i < 0 {
				breaks = append(breaks, fmt.Sprintf("%s/%s: no longer served", name, bv.Name))
				continue
			}
			bs, cs := bv.Schema.OpenAPIV3Schema, c.Spec.Versions[i].Schema.OpenAPIV3Schema
			if bs == nil {
				continue
			}
			compareSchema(name+"/"+bv.Name, bs, cs, &breaks)
		}
	}
	return breaks
}

// readCRDs reads every CRD in dir, by name.
func readCRDs(t *testing.T, dir string) map[string]crd {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs in %s: %v", dir, err)
	}
	crds := map[string]crd{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var c crd
		if err := utilyaml.Unmarshal(data, &c); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		crds[c.Metadata.Name] = c
	}
	return crds
}

// TestCRDsKeepTheLastRelease holds config/crd/bases to the promise against the
// last release's CRDs.
func TestCRDsKeepTheLastRelease(t *testing.T) {
	base := readCRDs(t, "baseline")
	cur := readCRDs(t, filepath.Join("..", "..", "config", "crd", "bases"))
	if breaks := compareCRDs(base, cur); len(breaks) > 0 {
		t.Errorf("the CRDs break the v1alpha1 compatibility promise (docs/design.md, \"Compatibility\"): "+
			"an object stored under the last release would stop validating. Add the field beside the old one, "+
			"or wait for v1beta1 (#1082):\n  %s", strings.Join(breaks, "\n  "))
	}
}

// parseSchema reads a schema written as YAML.
func parseSchema(t *testing.T, y string) *schema {
	t.Helper()
	var s schema
	if err := utilyaml.Unmarshal([]byte(y), &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

func TestCompareSchema(t *testing.T) {
	const base = `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string}
      replicas: {type: integer}
      mode: {type: string, enum: [Fast, Slow]}
      tags: {type: array, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
`
	cases := []struct {
		name string
		cur  string
		want []string
	}{
		{"unchanged", base, nil},
		{"an added optional field is accepted", `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string}
      replicas: {type: integer}
      mode: {type: string, enum: [Fast, Slow, Paused]}
      tags: {type: array, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
      paused: {type: boolean}
      limits: {type: object, required: [cpu], properties: {cpu: {type: string}}}
`, nil},
		{"a removed field is refused", `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string}
      mode: {type: string, enum: [Fast, Slow]}
      tags: {type: array, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
`, []string{".spec.replicas: removed"}},
		{"a retyped field is refused", `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string}
      replicas: {type: string}
      mode: {type: string, enum: [Fast, Slow]}
      tags: {type: array, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
`, []string{`.spec.replicas: type "integer" became "string"`}},
		{"a new required field is refused", `
type: object
properties:
  spec:
    type: object
    required: [name, replicas]
    properties:
      name: {type: string}
      replicas: {type: integer}
      mode: {type: string, enum: [Fast, Slow]}
      tags: {type: array, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
`, []string{".spec.replicas: newly required"}},
		{"a removed enum value is refused", `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string}
      replicas: {type: integer}
      mode: {type: string, enum: [Fast]}
      tags: {type: array, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
`, []string{`.spec.mode: enum value "Slow" removed`}},
		{"a field removed inside a list or map is refused", `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string}
      replicas: {type: integer}
      mode: {type: string, enum: [Fast, Slow]}
      tags: {type: array, items: {type: object}}
      labels: {type: object, additionalProperties: {type: integer}}
`, []string{`.spec.labels{}: type "string" became "integer"`, ".spec.tags[].key: removed"}},
		{"an enum on a free field is refused", `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string, enum: [a, b]}
      replicas: {type: integer}
      mode: {type: string, enum: [Fast, Slow]}
      tags: {type: array, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
`, []string{".spec.name: enum added"}},
		{"tightened bounds are refused", `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string, maxLength: 63, pattern: "^[a-z]+$"}
      replicas: {type: integer, minimum: 1}
      mode: {type: string, enum: [Fast, Slow]}
      tags: {type: array, x-kubernetes-list-type: set, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
`, []string{
			".spec.name: maxLength tightened", `.spec.name: pattern "" became "^[a-z]+$"`,
			".spec.replicas: minimum tightened", `.spec.tags: x-kubernetes-list-type "" became "set"`,
		}},
		{"a field widened to int-or-string is accepted", `
type: object
properties:
  spec:
    type: object
    required: [name]
    properties:
      name: {type: string}
      replicas: {x-kubernetes-int-or-string: true}
      mode: {type: string, enum: [Fast, Slow]}
      tags: {type: array, items: {type: object, properties: {key: {type: string}}}}
      labels: {type: object, additionalProperties: {type: string}}
`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			compareSchema("", parseSchema(t, base), parseSchema(t, c.cur), &got)
			if !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestCompareSchemaDefaults holds the guard to what a default and the flags
// mean (#1188): a default that changes or goes, a bound made exclusive, null
// or unknown fields no longer kept, each break the promise; an added default
// is left to review.
func TestCompareSchemaDefaults(t *testing.T) {
	const base = `
type: object
properties:
  spec:
    type: object
    properties:
      replicas: {type: integer, default: 1, minimum: 0, maximum: 10}
      mode: {type: string, default: Fast}
      planner: {type: object, default: {name: planner}, properties: {name: {type: string}}}
      extra: {type: object, nullable: true, x-kubernetes-preserve-unknown-fields: true}
      note: {type: string}
`
	cases := []struct {
		name string
		cur  string
		want []string
	}{
		{"unchanged", base, nil},
		{"a default spelled differently but equal is accepted", `
type: object
properties:
  spec:
    type: object
    properties:
      replicas: {type: integer, default: 1.0, minimum: 0, maximum: 10}
      mode: {type: string, default: "Fast"}
      planner:
        type: object
        default:
          name: planner
        properties: {name: {type: string}}
      extra: {type: object, nullable: true, x-kubernetes-preserve-unknown-fields: true}
      note: {type: string}
`, nil},
		{"an added default is accepted", `
type: object
properties:
  spec:
    type: object
    properties:
      replicas: {type: integer, default: 1, minimum: 0, maximum: 10}
      mode: {type: string, default: Fast}
      planner: {type: object, default: {name: planner}, properties: {name: {type: string}}}
      extra: {type: object, nullable: true, x-kubernetes-preserve-unknown-fields: true}
      note: {type: string, default: ""}
`, nil},
		{"a changed default is refused", `
type: object
properties:
  spec:
    type: object
    properties:
      replicas: {type: integer, default: 3, minimum: 0, maximum: 10}
      mode: {type: string, default: Slow}
      planner: {type: object, default: {name: architect}, properties: {name: {type: string}}}
      extra: {type: object, nullable: true, x-kubernetes-preserve-unknown-fields: true}
      note: {type: string}
`, []string{
			`.spec.mode: default "Fast" became "Slow"`,
			`.spec.planner: default {"name":"planner"} became {"name":"architect"}`,
			".spec.replicas: default 1 became 3",
		}},
		{"a removed default is refused", `
type: object
properties:
  spec:
    type: object
    properties:
      replicas: {type: integer, minimum: 0, maximum: 10}
      mode: {type: string, default: Fast}
      planner: {type: object, properties: {name: {type: string}}}
      extra: {type: object, nullable: true, x-kubernetes-preserve-unknown-fields: true}
      note: {type: string}
`, []string{`.spec.planner: default {"name":"planner"} removed`, ".spec.replicas: default 1 removed"}},
		{"a bound made exclusive is refused", `
type: object
properties:
  spec:
    type: object
    properties:
      replicas: {type: integer, default: 1, minimum: 0, exclusiveMinimum: true, maximum: 10, exclusiveMaximum: true}
      mode: {type: string, default: Fast}
      planner: {type: object, default: {name: planner}, properties: {name: {type: string}}}
      extra: {type: object, nullable: true, x-kubernetes-preserve-unknown-fields: true}
      note: {type: string}
`, []string{".spec.replicas: exclusiveMaximum tightened", ".spec.replicas: exclusiveMinimum tightened"}},
		{"null or unknown fields no longer kept are refused", `
type: object
properties:
  spec:
    type: object
    properties:
      replicas: {type: integer, default: 1, minimum: 0, maximum: 10}
      mode: {type: string, default: Fast}
      planner: {type: object, default: {name: planner}, properties: {name: {type: string}}}
      extra: {type: object}
      note: {type: string}
`, []string{
			".spec.extra: nullable true became false",
			".spec.extra: x-kubernetes-preserve-unknown-fields true became false",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			compareSchema("", parseSchema(t, base), parseSchema(t, c.cur), &got)
			if !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCompareCRDs(t *testing.T) {
	version := func(name string, served bool) string {
		return fmt.Sprintf(`{name: %s, served: %t, schema: {openAPIV3Schema: {type: object}}}`, name, served)
	}
	parse := func(versions ...string) crd {
		var c crd
		y := fmt.Sprintf("metadata: {name: things.c4.factory.io}\nspec: {versions: [%s]}", strings.Join(versions, ", "))
		if err := utilyaml.Unmarshal([]byte(y), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	base := map[string]crd{"things.c4.factory.io": parse(version("v1alpha1", true))}
	cases := []struct {
		name string
		cur  map[string]crd
		want []string
	}{
		{"a new served version is accepted",
			map[string]crd{"things.c4.factory.io": parse(version("v1alpha1", true), version("v1beta1", true))}, nil},
		{"a removed CRD is refused", map[string]crd{}, []string{"things.c4.factory.io: removed"}},
		{"a version no longer served is refused",
			map[string]crd{"things.c4.factory.io": parse(version("v1alpha1", false))},
			[]string{"things.c4.factory.io/v1alpha1: no longer served"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := compareCRDs(base, c.cur); !slices.Equal(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
