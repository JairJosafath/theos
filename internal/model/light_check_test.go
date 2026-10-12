package model

import (
	"slices"
	"testing"
)

func TestMakeTargets(t *testing.T) {
	makefile := "# A Makefile: check-light is in a comment\n" +
		"BINARY := app\n" +
		"FLAGS = -ldflags=-X:main\n" +
		"CC ::= gcc\n" +
		"WIN ?= x:y\n" +
		".PHONY: help check check-light\n" +
		"\n" +
		"help: ## List the targets.\n" +
		"\t@echo check-light: not a rule\n" +
		"check: fmt-check vet lint test ## Everything.\n" +
		"fmt-check vet:: deps\n" +
		"dist/%.tar.gz: build\n"
	got := MakeTargets([]byte(makefile))
	want := []string{".PHONY", "help", "check", "fmt-check", "vet", "dist/%.tar.gz"}
	if !slices.Equal(got, want) {
		t.Fatalf("MakeTargets = %q, want %q", got, want)
	}
}

func TestHasLightCheck(t *testing.T) {
	for _, tc := range []struct {
		name     string
		makefile string
		want     bool
	}{
		{"the Standard's rule", "check-light: fmt-check vet lint ## The light check.\n\tgo build ./...\n", true},
		{"named with others", "check check-light: deps\n", true},
		{"double colon", "check-light:: vet\n", true},
		{"a target of its own name only", "check-lighter: vet\ncheck: vet\n", false},
		// The reporter's Makefile: the Standard's update left it out, and it
		// has every target but check-light.
		{"left out without it", ".PHONY: help check check-light\nhelp:\ncheck: fmt-check vet lint test\n" +
			"vet-windows:\ntest-windows:\ntest-dist:\nbuild:\ndist:\nclean:\n", false},
		{"only in a recipe", "check:\n\tcheck-light: x\n", false},
		{"only in a comment", "# check-light: vet\ncheck: vet\n", false},
		{"only in a variable", "LIGHT := check-light: vet\n", false},
		{"no Makefile", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasLightCheck([]byte(tc.makefile)); got != tc.want {
				t.Errorf("HasLightCheck = %v, want %v", got, tc.want)
			}
		})
	}
}
