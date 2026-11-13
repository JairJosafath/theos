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
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A lease costs the on-demand price for the time it is held, a minute at
// least, rounded up to the thousandth of a dollar (T10).
func TestLeaseCostMilliUSD(t *testing.T) {
	const gpu = 1_006_000 // g5.xlarge: $1.006 an hour, in millionths
	for _, tt := range []struct {
		name    string
		price   int64
		held    time.Duration
		minimum time.Duration
		want    int64
	}{
		{"an hour", gpu, time.Hour, time.Minute, 1006},
		{"half an hour", gpu, 30 * time.Minute, time.Minute, 503},
		{"ten seconds, billed a minute", gpu, 10 * time.Second, time.Minute, 17},
		{"a second over a whole thousandth rounds up", 3_600_000, 1001 * time.Millisecond, 0, 1},
		{"three hours of a small machine", 41_600, 3 * time.Hour, time.Minute, 125},
		{"no price", 0, time.Hour, time.Minute, 0},
		{"not started", gpu, 0, 0, 0},
		{"a clock that went back", gpu, -time.Hour, 0, 0},
	} {
		if got := LeaseCostMilliUSD(tt.price, tt.held, tt.minimum); got != tt.want {
			t.Errorf("%s: LeaseCostMilliUSD(%d, %v, %v) = %d, want %d", tt.name, tt.price, tt.held, tt.minimum, got,
				tt.want)
		}
	}
	if got := BudgetMilliUSD(25); got != 25_000 {
		t.Errorf("BudgetMilliUSD(25) = %d, want 25000", got)
	}
}

// The public ranges are every IPv4 address but the private ranges, each
// once, in as few blocks as a security group's rules can name (T5).
func TestPublicRanges(t *testing.T) {
	ranges := PublicRanges()
	if len(ranges) > 50 {
		t.Errorf("PublicRanges has %d blocks, more than a security group's 60 rules leave room for", len(ranges))
	}
	blocks := make([]netip.Prefix, 0, len(ranges))
	var size uint64
	for _, r := range ranges {
		p := netip.MustParsePrefix(r)
		if p.Masked() != p {
			t.Errorf("%s is not a block's first address", r)
		}
		for _, b := range blocks {
			if b.Overlaps(p) {
				t.Errorf("%s overlaps %s", r, b)
			}
		}
		blocks = append(blocks, p)
		size += 1 << (32 - p.Bits())
	}
	var private uint64
	for _, r := range PrivateRanges() {
		p := netip.MustParsePrefix(r)
		private += 1 << (32 - p.Bits())
		for _, b := range blocks {
			if b.Overlaps(p) {
				t.Errorf("public block %s overlaps private %s", b, r)
			}
		}
	}
	if size+private != 1<<32 {
		t.Errorf("the public blocks and the private ranges cover %d addresses, want all %d", size+private, uint64(1)<<32)
	}
	for _, public := range []string{"1.1.1.1", "8.8.8.8", "172.32.0.1", "192.169.0.1", "203.0.113.7"} {
		addr := netip.MustParseAddr(public)
		found := false
		for _, b := range blocks {
			found = found || b.Contains(addr)
		}
		if !found {
			t.Errorf("no public block holds %s", public)
		}
	}
}

// A lease's certificate and key come from its token alone, the same bytes
// each time, so the operator pins what the machine serves; another token
// makes another pair (T4, T5).
func TestTestEnvCertificate(t *testing.T) {
	token := rand.Text()
	first, key, err := TestEnvCertificate(token)
	if err != nil {
		t.Fatal(err)
	}
	second, again, err := TestEnvCertificate(token)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !bytes.Equal(key, again) {
		t.Error("TestEnvCertificate made two pairs of one token")
	}
	other, _, err := TestEnvCertificate(rand.Text())
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Error("two tokens made one certificate")
	}
	block, _ := pem.Decode([]byte(first))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("TestEnvCertificate = %q, want a PEM certificate", first)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cert.PublicKey, key.Public()) || cert.IsCA ||
		!reflect.DeepEqual(cert.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Errorf("certificate %+v, want the key's, for a server, no CA", cert)
	}
	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		t.Errorf("the certificate isn't signed by its own key: %v", err)
	}
	pin, err := TestEnvKeyPin(token)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := base64.RawURLEncoding.DecodeString(pin); err != nil ||
		!bytes.Equal(raw, key.Public().(ed25519.PublicKey)) || len(pin) > 256 {
		t.Errorf("TestEnvKeyPin = %q, want the public key in a tag's value", pin)
	}
	if _, _, err := TestEnvCertificate(""); err == nil {
		t.Error("TestEnvCertificate of no token succeeded")
	}
}

// What a remote member's Secret lacks, and what CredentialsValid says, never
// the credential (T9).
func TestTestEnvCredentials(t *testing.T) {
	key := rand.Text()
	if got := TestEnvMissingKeys(TestEnvMemberAWS, map[string][]byte{"accessKeyId": []byte(key)}); !reflect.DeepEqual(
		got, []string{"secretAccessKey"}) {
		t.Errorf("TestEnvMissingKeys = %v, want secretAccessKey", got)
	}
	if got := TestEnvMissingKeys(TestEnvMemberLocal, nil); got != nil {
		t.Errorf("TestEnvMissingKeys(local) = %v, want none: a local environment needs no credential", got)
	}
	for _, tt := range []struct {
		reason  string
		missing []string
		detail  string
		want    []string
	}{
		{TestEnvCredentialsValid, nil, "", []string{"takes the credential in Secret gpu-aws"}},
		{TestEnvSecretMissing, nil, "", []string{"Secret gpu-aws, with the keys accessKeyId and secretAccessKey, is missing"}},
		{TestEnvSecretIncomplete, []string{"secretAccessKey"}, "", []string{"Secret gpu-aws lacks secretAccessKey"}},
		{TestEnvCredentialRefused, nil, "the credential is of AWS account 111111111111, the environment names 222222222222",
			[]string{"refuses the credential in Secret gpu-aws: the credential is of AWS account 111111111111"}},
		{TestEnvUnreachable, nil, "dial tcp: no such host", []string{"can't be reached", "no such host"}},
	} {
		got := TestEnvCredentialsMessage(tt.reason, "gpu-aws", tt.missing, tt.detail)
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("TestEnvCredentialsMessage(%s) = %q, want it to say %q", tt.reason, got, w)
			}
		}
	}
	if !TestEnvRemote(TestEnvMemberAWS) || TestEnvRemote(TestEnvMemberLocal) {
		t.Error("TestEnvRemote: aws is remote, local is not")
	}
}

// A lease's token goes into a shell script as it is only when it is made of
// safe characters, as crypto/rand.Text's are.
func TestLeaseTokenSafe(t *testing.T) {
	if !LeaseTokenSafe(rand.Text()) {
		t.Error("a crypto/rand.Text token isn't safe")
	}
	for _, unsafe := range []string{"", "a'b", "a b", "$(reboot)", "a\nb", "a;b"} {
		if LeaseTokenSafe(unsafe) {
			t.Errorf("LeaseTokenSafe(%q) = true", unsafe)
		}
	}
}
