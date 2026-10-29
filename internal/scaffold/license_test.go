package scaffold

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The schema's enum (api/v1beta1/shared_types.go, type License) and the
// embedded texts under licenses/ stay in step: every id has a text and every
// text an id (#1788).
func TestLicenseIDsMatchEnum(t *testing.T) {
	src, err := os.ReadFile("../../api/v1beta1/shared_types.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`\+kubebuilder:validation:Enum=([^\n]+)\ntype License string`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no Enum marker on type License in api/v1beta1/shared_types.go")
	}
	enum := strings.Split(strings.TrimSpace(string(m[1])), ";")
	slices.Sort(enum)
	if got := LicenseIDs(); !slices.Equal(got, enum) {
		t.Errorf("LicenseIDs() = %v, want the enum %v", got, enum)
	}
	for _, id := range enum {
		text, err := licenseText(License{ID: id, Holder: "Webshop", Year: 2026})
		if err != nil || len(text) == 0 {
			t.Errorf("licenseText(%s) = %d bytes, %v", id, len(text), err)
		}
	}
}

func TestResolveLicense(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                     string
		container, project       string
		displayName, projectName string
		want                     License
	}{
		{"none set", "", "", "Webshop", "webshop", License{}},
		{"the Project's", "", "MIT", "Webshop", "webshop", License{ID: "MIT", Holder: "Webshop", Year: 2026}},
		{"the Container's wins", "Apache-2.0", "MIT", "Webshop", "webshop",
			License{ID: "Apache-2.0", Holder: "Webshop", Year: 2026}},
		{"the Project's name when it has no displayName", "", "ISC", "", "webshop",
			License{ID: "ISC", Holder: "webshop", Year: 2026}},
	} {
		if got := ResolveLicense(tc.container, tc.project, tc.displayName, tc.projectName, now); got != tc.want {
			t.Errorf("%s: ResolveLicense() = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// Build writes LICENSE from the set license and records the id (#1788).
func TestBuildWritesLicense(t *testing.T) {
	for _, tc := range []struct {
		name     string
		license  License
		contains string
		identity string
	}{
		{"the copyright line is filled for MIT", License{ID: "MIT", Holder: "Webshop", Year: 2026},
			"Copyright (c) 2026 Webshop\n\nPermission is hereby granted", "MIT"},
		{"Apache-2.0 is written as published", License{ID: "Apache-2.0", Holder: "Webshop", Year: 2026},
			"Copyright [yyyy] [name of copyright owner]", "Apache-2.0"},
		{"0BSD's line is filled too", License{ID: "0BSD", Holder: "Webshop", Year: 2026},
			"Copyright (c) 2026 Webshop\n", "0BSD"},
		{"BSD-3-Clause keeps the period after the holder", License{ID: "BSD-3-Clause", Holder: "Webshop", Year: 2026},
			"Copyright (c) 2026 Webshop.\n", "BSD-3-Clause"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := ordersAPI
			in.License = tc.license
			res, err := Build(in, webshopLayers(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			text := string(res.Files[LicensePath].Data)
			if len(text) == 0 || !strings.Contains(text, tc.contains) {
				t.Errorf("LICENSE = %q, want it to contain %q", text, tc.contains)
			}
			id, err := ParseIdentity(res.Files[IdentityPath].Data)
			if err != nil {
				t.Fatal(err)
			}
			if id.License != tc.identity {
				t.Errorf("identity license = %q, want %q", id.License, tc.identity)
			}
		})
	}

	t.Run("none set writes none, and a layer may", func(t *testing.T) {
		layers := webshopLayers()
		layers[0].Files["LICENSE"] = File{Data: []byte("theirs")}
		res, err := Build(ordersAPI, layers, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(res.Files[LicensePath].Data); got != "theirs" {
			t.Errorf("LICENSE = %q, want the layer's", got)
		}
		res, err = Build(ordersAPI, webshopLayers(), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := res.Files[LicensePath]; ok {
			t.Error("LICENSE written with no license set")
		}
		id, _ := ParseIdentity(res.Files[IdentityPath].Data)
		if id.License != "" {
			t.Errorf("identity license = %q, want none", id.License)
		}
	})

	t.Run("a layer writing LICENSE while a license is set fails with the path", func(t *testing.T) {
		in := ordersAPI
		in.License = License{ID: "MIT", Holder: "Webshop", Year: 2026}
		layers := webshopLayers()
		layers[1].Files["LICENSE"] = File{Data: []byte("theirs")}
		_, err := Build(in, layers, Options{})
		if _, ok := errors.AsType[*ConflictError](err); !ok {
			t.Fatalf("Build() error = %v, want *ConflictError", err)
		}
		if want := "small and the operator both write LICENSE"; err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("an id without a text fails", func(t *testing.T) {
		in := ordersAPI
		in.License = License{ID: "WTFPL", Holder: "Webshop", Year: 2026}
		if _, err := Build(in, webshopLayers(), Options{}); err == nil || !strings.Contains(err.Error(), "WTFPL") {
			t.Errorf("Build() error = %v, want one naming WTFPL", err)
		}
	})
}
