package model

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// A remote test environment (docs/plans/done/test-environments.md, T5, T9 to
// T11): a machine a provider leases outside the cluster, such as an EC2
// instance, reached over public HTTPS with its certificate pinned, its
// credential a Secret only the operator reads, and its cost counted against
// the environment's budget.

// TestEnvMemberAWS is the aws member of a TestEnvironment: an EC2 machine per
// lease.
const TestEnvMemberAWS = "aws"

// TestEnvRemote reports whether member leases outside the cluster: a remote
// member needs a credential and a budget (T9, T10). Both remote members are
// AWS's, with its keys.
func TestEnvRemote(member string) bool {
	return member == TestEnvMemberAWS || member == TestEnvMemberDeviceFarm
}

// TestEnvRemotePort is the port theos-env serve listens on in a remote
// environment: HTTPS's own, the one port the sandbox fence lets out to a
// public address, so a run reaches the machine through the fence as it is.
const TestEnvRemotePort = 443

// AWSCredentialKeys are the keys a remote aws member's Secret must hold, as
// a cloudFormation DeployTarget's; sessionToken is optional (T9).
var AWSCredentialKeys = []string{"accessKeyId", "secretAccessKey"}

// TestEnvMissingKeys are the keys of member's credential that data lacks.
func TestEnvMissingKeys(member string, data map[string][]byte) []string {
	if !TestEnvRemote(member) {
		return nil
	}
	return missingKeys(AWSCredentialKeys, data)
}

// The reasons of a remote TestEnvironment's CredentialsValid condition, and
// the Ready reason it gives when it is False.
const (
	TestEnvCredentialsValid   = "Valid"
	TestEnvSecretMissing      = "SecretMissing"
	TestEnvSecretIncomplete   = "SecretIncomplete"
	TestEnvCredentialRefused  = "CredentialRefused" //nolint:gosec // a condition's reason, no credential
	TestEnvUnreachable        = "Unreachable"
	TestEnvCredentialsInvalid = "CredentialsInvalid"
)

// TestEnvCredentialsMessage is the CredentialsValid condition's message for
// reason: the Secret's name, the keys it lacks, and what the provider said,
// which never holds the credential itself.
func TestEnvCredentialsMessage(reason, secret string, missing []string, detail string) string {
	var msg string
	switch reason {
	case TestEnvCredentialsValid:
		msg = fmt.Sprintf("the provider takes the credential in Secret %s", secret)
	case TestEnvSecretMissing:
		msg = fmt.Sprintf("Secret %s, with the keys %s, is missing", secret, joinAnd(AWSCredentialKeys))
	case TestEnvSecretIncomplete:
		msg = fmt.Sprintf("Secret %s lacks %s", secret, joinAnd(missing))
	case TestEnvCredentialRefused:
		msg = fmt.Sprintf("the provider refuses the credential in Secret %s", secret)
	case TestEnvUnreachable:
		msg = "the provider can't be reached; it is asked again on the next poll"
	}
	if detail != "" {
		msg += ": " + detail
	}
	return msg
}

// EnvCredentialRefused is why a run waits for a remote environment whose
// credential its provider refuses, or whose Secret is missing or
// incomplete: a person mends it (T9).
const EnvCredentialRefused = "CredentialRefused" //nolint:gosec // a wait's reason, no credential

// BudgetMilliUSD is a day's budget of usd US dollars in thousandths; 0 is
// none.
func BudgetMilliUSD(usd int32) int64 { return int64(max(usd, 0)) * 1000 }

// LeaseCostMilliUSD is what a lease held for held cost, in thousandths of a
// US dollar, at microUSDPerHour, the on-demand price, billed for at least
// minimum (EC2 Linux bills a minute at least): rounded up, so a budget never
// counts less than the provider bills (T10).
func LeaseCostMilliUSD(microUSDPerHour int64, held, minimum time.Duration) int64 {
	seconds := int64(max(held, minimum, 0) / time.Second)
	if microUSDPerHour <= 0 || seconds <= 0 {
		return 0
	}
	const microSecondsPerMilliHour = 3600 * 1000
	return (microUSDPerHour*seconds + microSecondsPerMilliHour - 1) / microSecondsPerMilliHour
}

// PublicRanges are the IPv4 blocks outside PrivateRanges: where a remote
// environment's security group lets HTTPS out to, the private ranges left
// out as the local fence leaves them out (T5). A security group can only
// allow, so the complement is listed block by block.
func PublicRanges() []string {
	free := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	for _, r := range PrivateRanges() {
		private := netip.MustParsePrefix(r)
		next := make([]netip.Prefix, 0, len(free)+private.Bits())
		for _, block := range free {
			next = append(next, without(block, private)...)
		}
		free = next
	}
	slices.SortFunc(free, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	out := make([]string, len(free))
	for i, p := range free {
		out[i] = p.String()
	}
	return out
}

// without is block with hole taken out, as the fewest blocks.
func without(block, hole netip.Prefix) []netip.Prefix {
	switch {
	case !block.Overlaps(hole):
		return []netip.Prefix{block}
	case hole.Bits() <= block.Bits():
		return nil // the hole covers the block
	}
	// Halve the block and keep the half the hole isn't in whole.
	bits := block.Bits() + 1
	low := netip.PrefixFrom(block.Addr(), bits)
	high := netip.PrefixFrom(lastOf(low).Next(), bits)
	return append(without(low, hole), without(high, hole)...)
}

// lastOf is the last address of an IPv4 block.
func lastOf(p netip.Prefix) netip.Addr {
	a := p.Addr().As4()
	n := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	n |= ^uint32(0) >> p.Bits()
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

// testEnvCertInfo binds the key derived from a lease's token to its use.
const testEnvCertInfo = "theos-env tls certificate v1"

// TestEnvCertificate is the certificate and key a remote environment serves
// theos-env with, both derived from its lease's token, so the operator and
// the machine make the same pair and no key travels: the operator gives the
// run the certificate to pin (THEOS_ENV_<NAME>_CA), and the machine, given
// the token alone, serves with the key (`theos-env serve --tls`). Whoever
// can make the key holds the token already, and so the lease (T4, T5). The
// certificate names no host, since a machine's public address comes after
// its user data is written: the run pins the certificate itself.
func TestEnvCertificate(token string) (certPEM string, key ed25519.PrivateKey, err error) {
	if token == "" {
		return "", nil, fmt.Errorf("a lease with no token has no certificate")
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(testEnvCertInfo))
	seed := mac.Sum(nil)
	key = ed25519.NewKeyFromSeed(seed)
	serial := new(big.Int).SetBytes(seed[:16])
	serial.SetBit(serial, 127, 0) // positive, at most 16 bytes
	serial.Add(serial, big.NewInt(1))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "theos-env"},
		// Fixed, so both sides make the same bytes; the lease's deadline,
		// not the certificate, bounds how long it is used.
		NotBefore:             time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(nil, template, template, key.Public(), key)
	if err != nil {
		return "", nil, err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), key, nil
}

// TestEnvKeyPin is the public key of TestEnvCertificate(token), as a
// provider tags a machine with it so the operator, holding no token when it
// reads a lease back, can check that what answers there is the lease's
// theos-env: unpadded base64url of the Ed25519 key.
func TestEnvKeyPin(token string) (string, error) {
	_, key, err := TestEnvCertificate(token)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)), nil
}

// LeaseTokenSafe reports whether token is letters, digits, '-', '_' and '.'
// alone, as crypto/rand.Text makes it: one a provider can write into a
// remote machine's user data, a shell script, as it is.
func LeaseTokenSafe(token string) bool {
	return token != "" && !strings.ContainsFunc(token, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.'
	})
}
