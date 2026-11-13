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

package model

import (
	"strings"
	"testing"
)

func TestAlertSecretMissing(t *testing.T) {
	val := []byte("v")
	for _, tc := range []struct {
		name, typ string
		data      map[string][]byte
		key       string
		missing   string // a part of the message; "" when the Secret checks deliveries
	}{
		{"alertmanager with a token", AlertTypeAlertmanager, map[string][]byte{"token": val}, "token", ""},
		{"alertmanager reads no secret", AlertTypeAlertmanager, map[string][]byte{"secret": val}, "token",
			`has no "token"`},
		{"alertmanager with an empty token", AlertTypeAlertmanager, map[string][]byte{"token": {}}, "token",
			`has an empty "token", which is no credential`},
		{"sentry with a secret", AlertTypeSentry, map[string][]byte{"secret": val}, "secret", ""},
		{"sentry reads no token", AlertTypeSentry, map[string][]byte{"token": val}, "secret", `has no "secret"`},
		{"sentry with an empty secret", AlertTypeSentry, map[string][]byte{"secret": nil}, "secret",
			`has an empty "secret"`},
		{"generic with a secret", AlertTypeGeneric, map[string][]byte{"secret": val}, "secret", ""},
		{"generic with a token", AlertTypeGeneric, map[string][]byte{"token": val}, "token", ""},
		{"generic with both reads the secret", AlertTypeGeneric, map[string][]byte{"secret": val, "token": val},
			"secret", ""},
		// The secret is the one generic reads when it is there: an empty one
		// is not passed over for the token, or an HMAC anyone can compute
		// would be what the Secret means.
		{"generic with an empty secret and a token", AlertTypeGeneric,
			map[string][]byte{"secret": {}, "token": val}, "secret", `has an empty "secret"`},
		{"generic with neither", AlertTypeGeneric, map[string][]byte{"other": val}, "token",
			`has no "secret" or "token"`},
		{"generic with an empty Secret", AlertTypeGeneric, nil, "token", `has no "secret" or "token"`},
		{"an unknown type", "nagios", map[string][]byte{"token": val}, "", "has no key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AlertSecretKey(tc.typ, tc.data); got != tc.key {
				t.Errorf("AlertSecretKey = %q, want %q", got, tc.key)
			}
			got := AlertSecretMissing(tc.typ, "alerts", tc.data)
			switch {
			case tc.missing == "" && got != "":
				t.Errorf("AlertSecretMissing = %q, want none", got)
			case tc.missing != "" && !strings.Contains(got, tc.missing):
				t.Errorf("AlertSecretMissing = %q, want it to say %q", got, tc.missing)
			}
		})
	}
}
