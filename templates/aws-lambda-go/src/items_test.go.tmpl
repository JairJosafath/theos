package main

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckID(t *testing.T) {
	for _, id := range []string{"a", "lamp-1", "A_b-9", strings.Repeat("x", 64)} {
		if got, err := checkID(id); err != nil || got != id {
			t.Errorf("checkID(%q) = %q, %v; want it back", id, got, err)
		}
	}
	for _, id := range []string{"", strings.Repeat("x", 65), "a b", "a/b", "é", "a.b"} {
		if _, err := checkID(id); !errors.Is(err, ErrBadID) {
			t.Errorf("checkID(%q) error = %v, want ErrBadID", id, err)
		}
	}
}

func TestCheckItemAcceptsAnItem(t *testing.T) {
	for body, want := range map[string]Item{
		`{"name":"Lamp","quantity":3}`:                {ID: "a", Name: "Lamp", Quantity: 3},
		`{"name":"  Lamp  ","quantity":0}`:            {ID: "a", Name: "Lamp", Quantity: 0},
		`{"name":"Lamp","quantity":3.0}`:              {ID: "a", Name: "Lamp", Quantity: 3},
		`{"name":"Lamp","quantity":9007199254740991}`: {ID: "a", Name: "Lamp", Quantity: 9007199254740991},
		`{"name":"Lamp","quantity":1,"colour":"red"}`: {ID: "a", Name: "Lamp", Quantity: 1},
	} {
		got, err := checkItem("a", body)
		if err != nil || got != want {
			t.Errorf("checkItem(%s) = %+v, %v; want %+v", body, got, err, want)
		}
	}
}

func TestCheckItemRefusesABadBody(t *testing.T) {
	for body, want := range map[string]string{
		``:                                 "the body must be JSON",
		`{`:                                "the body must be JSON",
		`null`:                             "the body must be a JSON object",
		`[1]`:                              "the body must be a JSON object",
		`"lamp"`:                           "the body must be a JSON object",
		`{"quantity":3}`:                   ErrBadName.Error(),
		`{"name":"","quantity":3}`:         ErrBadName.Error(),
		`{"name":"   ","quantity":3}`:      ErrBadName.Error(),
		`{"name":5,"quantity":3}`:          ErrBadName.Error(),
		`{"name":"Lamp"}`:                  ErrBadQuantity.Error(),
		`{"name":"Lamp","quantity":-1}`:    ErrBadQuantity.Error(),
		`{"name":"Lamp","quantity":1.5}`:   ErrBadQuantity.Error(),
		`{"name":"Lamp","quantity":"3"}`:   ErrBadQuantity.Error(),
		`{"name":"Lamp","quantity":null}`:  ErrBadQuantity.Error(),
		`{"name":"Lamp","quantity":1e300}`: ErrBadQuantity.Error(),
		`{"name":"Lamp","quantity":9007199254740992}`: ErrBadQuantity.Error(),
	} {
		if _, err := checkItem("a", body); err == nil || err.Error() != want {
			t.Errorf("checkItem(%s) error = %v, want %q", body, err, want)
		}
	}
}

func TestCheckFieldsBoundsTheName(t *testing.T) {
	if _, err := checkFields("a", strings.Repeat("é", maxNameLength), 1); err != nil {
		t.Errorf("a name of %d characters is refused: %v", maxNameLength, err)
	}
	if _, err := checkFields("a", strings.Repeat("é", maxNameLength+1), 1); !errors.Is(err, ErrBadName) {
		t.Errorf("a name of %d characters: error = %v, want ErrBadName", maxNameLength+1, err)
	}
}
