/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package main

import "testing"

func TestParseUpstream(t *testing.T) {
	for _, tt := range []struct {
		raw string
		ok  bool
	}{
		{raw: "https://api.anthropic.com", ok: true},
		{raw: "http://claude-api.test:8080/base", ok: true},
		{raw: "api.anthropic.com"},
		{raw: "ftp://api.anthropic.com"},
		{raw: "https://"},
		{raw: "https://api.anthropic.com/%zz"},
	} {
		if _, err := parseUpstream(tt.raw); (err == nil) != tt.ok {
			t.Errorf("parseUpstream(%q) error = %v; want ok %v", tt.raw, err, tt.ok)
		}
	}
}
