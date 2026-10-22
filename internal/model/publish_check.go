package model

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// Reasons a release's source isn't published: the checks before it goes out
// (PublishChecks). Each holds the release until another one is wanted, as
// PublishTagTaken does.
const (
	PublishLicenseMissing = "LicenseMissing"
	PublishReadmeMissing  = "ReadmeMissing"
	PublishSecretFound    = "SecretFound"
)

// AllowSecretMark on a line of a source file skips that line's secret check:
// a test fixture, a sample key.
const AllowSecretMark = "c4factory:allow-secret" //nolint:gosec // a mark, not a credential

// The files at a source's root that count as its license and its README.
var (
	licenseNames = []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"}
	readmeNames  = []string{"README.md", "README"}
)

// PublishProblem is one thing that stops a release's source going out: its
// reason, and for a secret the file, the line and the rule that found it.
type PublishProblem struct {
	Reason string
	Path   string
	Line   int
	Rule   string
}

// String names the problem: for a secret its file, line and rule, never the
// value.
func (p PublishProblem) String() string {
	switch p.Reason {
	case PublishLicenseMissing:
		return "no license: add a non-empty " + orList(licenseNames) + " at the root"
	case PublishReadmeMissing:
		return "no README: add a non-empty " + orList(readmeNames) + " at the root"
	}
	return fmt.Sprintf("%s line %d: %s", p.Path, p.Line, p.Rule)
}

func orList(names []string) string {
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// secretRule is a token shape, named after its issuer.
type secretRule struct {
	name string
	re   *regexp.Regexp
}

// secretRules are high-precision token shapes: a hit stops a release with no
// person in the loop, so each is a prefix its issuer reserves and the length
// and alphabet it issues.
var secretRules = []secretRule{
	{"a private key", regexp.MustCompile(`-----BEGIN[A-Z0-9 ]* PRIVATE KEY( BLOCK)?-----`)},
	{"a GitHub token (ghp_)", regexp.MustCompile(`\bghp_[A-Za-z0-9]{36}\b`)},
	{"a GitHub token (gho_)", regexp.MustCompile(`\bgho_[A-Za-z0-9]{36}\b`)},
	{"a GitHub token (ghs_)", regexp.MustCompile(`\bghs_[A-Za-z0-9]{36}\b`)},
	{"a GitHub token (github_pat_)", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9]{22}_[A-Za-z0-9]{59}\b`)},
	{"an AWS access key id (AKIA)", regexp.MustCompile(`\bAKIA[A-Z2-7]{16}\b`)},
	{"an AWS access key id (ASIA)", regexp.MustCompile(`\bASIA[A-Z2-7]{16}\b`)},
	{"a Slack token (xox)", regexp.MustCompile(`\bxox[abprs]-[0-9]{6,}-[A-Za-z0-9-]{10,}`)},
	{"a Google API key (AIza)", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"a Stripe secret key (sk_live_)", regexp.MustCompile(`\bsk_live_[0-9A-Za-z]{24,}`)},
	{"an npm token (npm_)", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`)},
	{"a PyPI token (pypi-)", regexp.MustCompile(`\bpypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{50,}`)},
	{"an Anthropic key (sk-ant-)", regexp.MustCompile(`\bsk-ant-[a-z]+[0-9]{2}-[A-Za-z0-9_-]{80,}`)},
}

// ruleKnown names a hit of a credential the operator holds.
const ruleKnown = "a credential the operator holds"

// minKnown is the shortest known value compared: a shorter one would match
// ordinary text.
const minKnown = 8

// scanWindow is the most of a line held at once; a longer line is scanned in
// windows that overlap by scanOverlap, longer than any token compared.
const (
	scanWindow  = 64 << 10
	scanOverlap = 4 << 10
)

// SecretScan is what ScanSecrets found in a file: a problem per line holding
// a secret, and the lines it skipped for their AllowSecretMark.
type SecretScan struct {
	Found   []PublishProblem
	Allowed int
}

// ScanSecrets reads the file at path line by line from content, to its end,
// and reports each line holding a secret: a private key block, a token of a
// shape secretRules know, or one of known, the values of credentials the
// operator holds (shorter than minKnown, or a number, are left out). A line
// is reported once, for the first rule that hits; one marked AllowSecretMark
// is counted as allowed instead. A file with a NUL byte is binary: it is read
// to its end and reports nothing.
func ScanSecrets(path string, content io.Reader, known []string) (SecretScan, error) {
	var values [][]byte
	for _, k := range known {
		// A number (a GitHub App's installationId) is no secret, and text
		// holds numbers.
		if len(k) >= minKnown && strings.Trim(k, "0123456789") != "" {
			values = append(values, []byte(k))
		}
	}
	var scan SecretScan
	r := bufio.NewReaderSize(content, scanWindow)
	var window, carry []byte
	line, rule, marked := 1, "", false
	for {
		chunk, err := r.ReadSlice('\n')
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
			return SecretScan{}, err
		}
		if bytes.IndexByte(chunk, 0) >= 0 {
			_, err := io.Copy(io.Discard, r)
			return SecretScan{}, err
		}
		window = append(append(window[:0], carry...), chunk...)
		marked = marked || bytes.Contains(window, []byte(AllowSecretMark))
		if rule == "" {
			rule = matchSecret(window, values)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			carry = append(carry[:0], window[max(0, len(window)-scanOverlap):]...)
			continue
		}
		switch {
		case rule != "" && marked:
			scan.Allowed++
		case rule != "":
			scan.Found = append(scan.Found, PublishProblem{Reason: PublishSecretFound, Path: path, Line: line, Rule: rule})
		}
		if err != nil {
			return scan, nil
		}
		line, rule, marked, carry = line+1, "", false, carry[:0]
	}
}

// matchSecret is the name of the first rule that hits in text, or "".
func matchSecret(text []byte, known [][]byte) string {
	for _, r := range secretRules {
		if r.re.Match(text) {
			return r.name
		}
	}
	for _, k := range known {
		if bytes.Contains(text, k) {
			return ruleKnown
		}
	}
	return ""
}

// CheckedFile is a file of a release's source as the checks see it: its
// path, its size and what ScanSecrets found in it.
type CheckedFile struct {
	Path    string
	Size    int64
	Secrets SecretScan
}

// PublishChecks are the problems that stop a release's source going out,
// over the files about to be published (FactoryOwnPaths left out): every
// secret found, by path and line, then a missing license, then a missing
// README. Both count only at the root, and only non-empty.
func PublishChecks(files []CheckedFile) []PublishProblem {
	var problems []PublishProblem
	for _, f := range files {
		problems = append(problems, f.Secrets.Found...)
	}
	slices.SortFunc(problems, func(a, b PublishProblem) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
	})
	if !hasRootFile(files, licenseNames) {
		problems = append(problems, PublishProblem{Reason: PublishLicenseMissing})
	}
	if !hasRootFile(files, readmeNames) {
		problems = append(problems, PublishProblem{Reason: PublishReadmeMissing})
	}
	return problems
}

func hasRootFile(files []CheckedFile, names []string) bool {
	return slices.ContainsFunc(files, func(f CheckedFile) bool {
		return f.Size > 0 && slices.Contains(names, f.Path)
	})
}

// AllowedSecrets is how many lines of files AllowSecretMark skipped.
func AllowedSecrets(files []CheckedFile) int {
	n := 0
	for _, f := range files {
		n += f.Secrets.Allowed
	}
	return n
}

// PublishProblemsMessage lists every problem, the files' first, so one pass
// fixes them all; with no secret found it says how many lines allowed
// skipped.
func PublishProblemsMessage(problems []PublishProblem, allowed int) string {
	said := make([]string, 0, len(problems))
	secrets := false
	for _, p := range problems {
		said = append(said, p.String())
		secrets = secrets || p.Reason == PublishSecretFound
	}
	msg := strings.Join(said, "; ")
	if !secrets && allowed > 0 {
		msg += fmt.Sprintf(" (%d lines marked %s were skipped)", allowed, AllowSecretMark)
	}
	return msg
}

// Check runs PublishChecks over the files of s, leaving out those it doesn't
// publish, into s.Problems and s.Allowed.
func (s *ReleaseSource) Check(files []CheckedFile) {
	published := make(map[string]bool, len(s.Files))
	for _, f := range s.Files {
		published[f.Path] = true
	}
	kept := slices.DeleteFunc(slices.Clone(files), func(f CheckedFile) bool { return !published[f.Path] })
	s.Problems, s.Allowed = PublishChecks(kept), AllowedSecrets(kept)
}

// ScanBlob reads the size bytes of the file at path from r once, as a
// stream, into its git blob SHA-1 (HashBlob) and what ScanSecrets finds in it.
func ScanBlob(path string, r io.Reader, size int64, known []string) (string, SecretScan, error) {
	h := blobHasher(size)
	n := new(byteCount)
	scan, err := ScanSecrets(path, io.TeeReader(r, io.MultiWriter(h, n)), known)
	switch {
	case err != nil:
		return "", SecretScan{}, err
	case int64(*n) != size:
		return "", SecretScan{}, fmt.Errorf("read %d bytes of a %d byte file", *n, size)
	}
	return hex.EncodeToString(h.Sum(nil)), scan, nil
}

// byteCount counts the bytes written to it.
type byteCount int64

func (c *byteCount) Write(p []byte) (int, error) {
	*c += byteCount(len(p))
	return len(p), nil
}
