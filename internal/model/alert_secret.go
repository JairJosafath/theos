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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// The keys of an AlertSource's Secret (docs/plans/alerts.md, decision 3).
const (
	// AlertSecretKeyToken holds a bearer token: Alertmanager's, and a
	// generic sender's when the Secret has no AlertSecretKeySecret.
	AlertSecretKeyToken = "token"
	// AlertSecretKeySecret holds an HMAC key: Sentry's client secret, and a
	// generic sender's.
	AlertSecretKeySecret = "secret"
)

// AlertSecretKey is the key of an AlertSource's Secret that checks the
// deliveries of its type: token for alertmanager, secret for sentry, and for
// generic secret when the Secret has that key, else token. "" for a type
// there is no such key for.
func AlertSecretKey(typ string, data map[string][]byte) string {
	switch typ {
	case AlertTypeAlertmanager:
		return AlertSecretKeyToken
	case AlertTypeSentry:
		return AlertSecretKeySecret
	case AlertTypeGeneric:
		if _, ok := data[AlertSecretKeySecret]; ok {
			return AlertSecretKeySecret
		}
		return AlertSecretKeyToken
	}
	return ""
}

// AlertSecretMissing says why the Secret secret, whose data is data, can't
// check the deliveries of an AlertSource of type typ, or "" when it can. An
// empty value is no credential: an empty bearer token would equal an empty
// token, and an HMAC with an empty key is one anyone can compute.
func AlertSecretMissing(typ, secret string, data map[string][]byte) string {
	key := AlertSecretKey(typ, data)
	switch {
	case key == "":
		return fmt.Sprintf("An AlertSource of type %q has no key to check its deliveries with", typ)
	case len(data[key]) > 0:
		return ""
	}
	want := fmt.Sprintf("%q", key)
	if typ == AlertTypeGeneric && key == AlertSecretKeyToken {
		want = fmt.Sprintf("%q or %q", AlertSecretKeySecret, AlertSecretKeyToken)
	}
	if _, ok := data[key]; ok {
		return fmt.Sprintf("Secret %s has an empty %q, which is no credential; a %s source checks its "+
			"deliveries with %s: until it holds a value, every delivery is refused", secret, key, typ, want)
	}
	return fmt.Sprintf("Secret %s has no %s, which a %s source checks its deliveries with: until it has, "+
		"every delivery is refused", secret, want, typ)
}

// The headers a delivery carries its credential in (decision 3).
const (
	// AlertHeaderAuthorization carries "Bearer <token>": Alertmanager's,
	// and a generic sender's whose Secret has a token alone.
	AlertHeaderAuthorization = "Authorization"
	// AlertHeaderSentrySignature carries Sentry's hex HMAC-SHA256 of the
	// body with the integration's client secret.
	AlertHeaderSentrySignature = "Sentry-Hook-Signature"
	// AlertHeaderSignature carries a generic sender's "sha256=<hex
	// HMAC-SHA256 of the body>".
	AlertHeaderSignature = "X-Theos-Signature"
)

// AlertDeliveryAuthentic says whether a delivery to an AlertSource of type
// typ, whose Secret's data is data, carries what that Secret checks: a
// bearer token equal to token, or an HMAC-SHA256 of body under secret, in
// the header its type reads (header reads one by name). The key is
// AlertSecretKey's, the one the source's Ready condition checks, and a
// Secret AlertSecretMissing refuses authenticates nothing. Comparisons are
// constant-time, and a token is compared by its SHA-256 digest, so the
// comparison doesn't tell its length.
func AlertDeliveryAuthentic(typ string, data map[string][]byte, header func(string) string, body []byte) bool {
	key := AlertSecretKey(typ, data)
	if key == "" || len(data[key]) == 0 {
		return false
	}
	want := data[key]
	switch {
	case key == AlertSecretKeyToken:
		token, ok := strings.CutPrefix(header(AlertHeaderAuthorization), "Bearer ")
		if !ok {
			return false
		}
		got, expected := sha256.Sum256([]byte(token)), sha256.Sum256(want)
		return subtle.ConstantTimeCompare(got[:], expected[:]) == 1
	case typ == AlertTypeSentry:
		return alertHMACEqual(header(AlertHeaderSentrySignature), want, body)
	default:
		sig, ok := strings.CutPrefix(header(AlertHeaderSignature), "sha256=")
		return ok && alertHMACEqual(sig, want, body)
	}
}

// alertHMACEqual says whether sig, hex, is the HMAC-SHA256 of body under key.
func alertHMACEqual(sig string, key, body []byte) bool {
	got, err := hex.DecodeString(strings.TrimSpace(sig))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
