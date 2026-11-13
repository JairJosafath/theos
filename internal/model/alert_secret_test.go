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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestAlertDeliveryAuthentic(t *testing.T) {
	token, key, other := rand.Text(), rand.Text(), rand.Text()
	body := []byte(`{"title":"disk full"}`)
	sign := func(k string, b []byte) string {
		mac := hmac.New(sha256.New, []byte(k))
		mac.Write(b)
		return hex.EncodeToString(mac.Sum(nil))
	}
	bearer := func(t string) map[string]string { return map[string]string{"Authorization": "Bearer " + t} }
	tokens := map[string][]byte{"token": []byte(token)}
	secrets := map[string][]byte{"secret": []byte(key)}
	both := map[string][]byte{"secret": []byte(key), "token": []byte(token)}
	for _, tc := range []struct {
		name, typ string
		data      map[string][]byte
		headers   map[string]string
		body      []byte
		want      bool
	}{
		{"alertmanager with its bearer token", AlertTypeAlertmanager, tokens, bearer(token), body, true},
		{"alertmanager with another token", AlertTypeAlertmanager, tokens, bearer(other), body, false},
		{"alertmanager with the token cut short", AlertTypeAlertmanager, tokens, bearer(token[:len(token)-1]),
			body, false},
		{"alertmanager with the token but no Bearer", AlertTypeAlertmanager, tokens,
			map[string]string{"Authorization": token}, body, false},
		{"alertmanager with no header", AlertTypeAlertmanager, tokens, nil, body, false},
		// An empty token is no credential: "Bearer " would equal it.
		{"alertmanager whose token is empty", AlertTypeAlertmanager, map[string][]byte{"token": {}}, bearer(""),
			body, false},
		{"alertmanager reads no signature", AlertTypeAlertmanager, secrets,
			map[string]string{"X-Theos-Signature": "sha256=" + sign(key, body)}, body, false},
		{"sentry signed with its secret", AlertTypeSentry, secrets,
			map[string]string{"Sentry-Hook-Signature": sign(key, body)}, body, true},
		{"sentry signed with another secret", AlertTypeSentry, secrets,
			map[string]string{"Sentry-Hook-Signature": sign(other, body)}, body, false},
		{"sentry with the body changed", AlertTypeSentry, secrets,
			map[string]string{"Sentry-Hook-Signature": sign(key, body)}, []byte(`{"title":"disk ok"}`), false},
		{"sentry with a signature that is no hex", AlertTypeSentry, secrets,
			map[string]string{"Sentry-Hook-Signature": "not-hex"}, body, false},
		{"sentry with no signature", AlertTypeSentry, secrets, nil, body, false},
		// An HMAC with an empty key is one anyone can compute.
		{"sentry whose secret is empty", AlertTypeSentry, map[string][]byte{"secret": {}},
			map[string]string{"Sentry-Hook-Signature": sign("", body)}, body, false},
		{"sentry takes no bearer token", AlertTypeSentry, both, bearer(token), body, false},
		{"generic signed with its secret", AlertTypeGeneric, secrets,
			map[string]string{"X-Theos-Signature": "sha256=" + sign(key, body)}, body, true},
		{"generic signed without the sha256= prefix", AlertTypeGeneric, secrets,
			map[string]string{"X-Theos-Signature": sign(key, body)}, body, false},
		{"generic signed with another secret", AlertTypeGeneric, secrets,
			map[string]string{"X-Theos-Signature": "sha256=" + sign(other, body)}, body, false},
		{"generic with a token alone", AlertTypeGeneric, tokens, bearer(token), body, true},
		{"generic with both takes no token", AlertTypeGeneric, both, bearer(token), body, false},
		{"generic with both takes the signature", AlertTypeGeneric, both,
			map[string]string{"X-Theos-Signature": "sha256=" + sign(key, body)}, body, true},
		// An empty secret beside a token doesn't fall back to the token.
		{"generic whose secret is empty beside a token", AlertTypeGeneric,
			map[string][]byte{"secret": {}, "token": []byte(token)},
			map[string]string{"Authorization": "Bearer " + token, "X-Theos-Signature": "sha256=" + sign("", body)},
			body, false},
		{"generic whose token is empty", AlertTypeGeneric, map[string][]byte{"token": {}}, bearer(""), body, false},
		{"generic with no Secret", AlertTypeGeneric, nil, bearer(token), body, false},
		{"an unknown type", "nagios", tokens, bearer(token), body, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := func(name string) string { return tc.headers[name] }
			if got := AlertDeliveryAuthentic(tc.typ, tc.data, header, tc.body); got != tc.want {
				t.Errorf("AlertDeliveryAuthentic = %v, want %v", got, tc.want)
			}
		})
	}
}

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
