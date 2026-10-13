package main

// The API's rules, free of AWS: what an item is, which ids and bodies are
// valid. Nothing here imports the AWS SDK or the Lambda events, so the tests
// need neither.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Item is an item as the API stores and returns it.
type Item struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Quantity int64  `json:"quantity"`
}

// Ids end up in URLs and DynamoDB keys: a short, URL-safe alphabet.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// maxNameLength bounds a name, which is for people to read; DynamoDB items
// can be 400 KB, names can't.
const maxNameLength = 200

// maxQuantity is the largest quantity a client can send and read back
// exactly: JavaScript's Number.MAX_SAFE_INTEGER.
const maxQuantity = 1<<53 - 1

var (
	// ErrBadID is returned for an id outside idPattern.
	ErrBadID = errors.New(`the id must be 1 to 64 letters, digits, "-" or "_"`)
	// ErrBadName is returned for a missing, blank or too long name.
	ErrBadName = fmt.Errorf(`"name" must be a non-empty string of at most %d characters`, maxNameLength)
	// ErrBadQuantity is returned for a quantity that is not a whole number, 0 or more.
	ErrBadQuantity = errors.New(`"quantity" must be a whole number, 0 or more`)
)

// checkID checks an id from the path.
func checkID(raw string) (string, error) {
	if !idPattern.MatchString(raw) {
		return "", ErrBadID
	}
	return raw, nil
}

// checkItem checks a request body: a JSON object with a name and a quantity.
func checkItem(id, body string) (Item, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return Item{}, errors.New("the body must be a JSON object")
		}
		return Item{}, errors.New("the body must be JSON")
	}
	if fields == nil {
		return Item{}, errors.New("the body must be a JSON object")
	}
	var name string
	if err := json.Unmarshal(fields["name"], &name); err != nil {
		return Item{}, ErrBadName
	}
	quantity, err := wholeNumber(fields["quantity"])
	if err != nil {
		return Item{}, err
	}
	return checkFields(id, name, quantity)
}

// wholeNumber reads a JSON number that is a whole number, such as 3 or 3.0,
// within maxQuantity. A string such as "3" is not a number.
func wholeNumber(raw json.RawMessage) (int64, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, ErrBadQuantity
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, ErrBadQuantity
	}
	f, err := n.Float64()
	if err != nil || f < 0 || f > maxQuantity || f != math.Trunc(f) {
		return 0, ErrBadQuantity
	}
	return int64(f), nil
}

// checkFields checks an item's fields and trims its name. The store calls it
// too, on what DynamoDB returns, so a row written by something else can't
// reach a client unchecked.
func checkFields(id, name string, quantity int64) (Item, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return Item{}, ErrBadName
	}
	if quantity < 0 || quantity > maxQuantity {
		return Item{}, ErrBadQuantity
	}
	return Item{ID: id, Name: name, Quantity: quantity}, nil
}
