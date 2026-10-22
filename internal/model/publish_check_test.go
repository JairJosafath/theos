package model

import (
	"crypto/rand"
	"reflect"
	"strings"
	"testing"
)

// token is a made-up token of prefix and n characters of alphabet, random
// each run, so no credential shape sits in the repository (the secrets
// skill).
func token(prefix string, n int, alphabet string) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return prefix + string(b)
}

const (
	alnum  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	upper  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	urlB64 = alnum + "-_"
)

// #1602: each rule finds its own shape, names the file, the line and the
// rule, and never the value.
func TestScanSecrets(t *testing.T) {
	known := token("", 24, alnum)
	keyBlock := "-----BEGIN " + "RSA PRIVATE KEY-----"
	cases := []struct {
		name, line, rule string
	}{
		{"private key", keyBlock, "a private key"},
		{"openssh private key", "-----BEGIN " + "OPENSSH PRIVATE KEY-----", "a private key"},
		{"github ghp_", "token: " + token("gh"+"p_", 36, alnum), "a GitHub token (ghp_)"},
		{"github gho_", token("gh"+"o_", 36, alnum), "a GitHub token (gho_)"},
		{"github ghs_", token("gh"+"s_", 36, alnum), "a GitHub token (ghs_)"},
		{"github fine-grained", token(token("github"+"_pat_", 22, alnum)+"_", 59, alnum),
			"a GitHub token (github_pat_)"},
		{"aws AKIA", "AWS_ACCESS_KEY_ID=" + token("AK"+"IA", 16, upper), "an AWS access key id (AKIA)"},
		{"aws ASIA", token("AS"+"IA", 16, upper), "an AWS access key id (ASIA)"},
		{"slack", token("xo"+"xb-123456789012-", 24, alnum), "a Slack token (xox)"},
		{"google", "key=" + token("AI"+"za", 35, urlB64), "a Google API key (AIza)"},
		{"stripe", token("sk"+"_live_", 24, alnum), "a Stripe secret key (sk_live_)"},
		{"npm", "//registry.npmjs.org/:_authToken=" + token("np"+"m_", 36, alnum), "an npm token (npm_)"},
		{"pypi", token("py"+"pi-AgEIcHlwaS5vcmc", 60, urlB64), "a PyPI token (pypi-)"},
		{"anthropic", token("sk"+"-ant-api03-", 93, urlB64), "an Anthropic key (sk-ant-)"},
		{"known value", "password = " + known, "a credential the operator holds"},
		{"plain text", "nothing to see here: ghp_short AKIA sk_live_", ""},
		{"a token inside a longer word", "x" + token("gh"+"p_", 40, alnum), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			content := "first line\n" + c.line + "\nlast line"
			scan, err := ScanSecrets("config/app.env", strings.NewReader(content), []string{known, "short", ""})
			if err != nil {
				t.Fatal(err)
			}
			if c.rule == "" {
				if len(scan.Found) != 0 {
					t.Fatalf("found %v", scan.Found)
				}
				return
			}
			want := []PublishProblem{{Reason: PublishSecretFound, Path: "config/app.env", Line: 2, Rule: c.rule}}
			if !reflect.DeepEqual(scan.Found, want) {
				t.Fatalf("found %v, want %v", scan.Found, want)
			}
			said := PublishProblemsMessage(scan.Found, 0)
			if !strings.Contains(said, "config/app.env line 2: "+c.rule) {
				t.Errorf("message %q doesn't name the file, line and rule", said)
			}
			if strings.Contains(said, c.line) || strings.Contains(said, known) {
				t.Errorf("message %q holds the value", said)
			}
		})
	}
}

func TestScanSecretsAllowMarkAndPlainKnown(t *testing.T) {
	ghp := token("gh"+"p_", 36, alnum)
	content := ghp + " # " + AllowSecretMark + "\n" + "short and plain\n" + ghp + "\n"
	scan, err := ScanSecrets("a.txt", strings.NewReader(content+"id 123456789\n"), []string{"short", "123456789"})
	if err != nil {
		t.Fatal(err)
	}
	want := SecretScan{Allowed: 1, Found: []PublishProblem{{Reason: PublishSecretFound, Path: "a.txt", Line: 3,
		Rule: "a GitHub token (ghp_)"}}}
	if !reflect.DeepEqual(scan, want) {
		t.Fatalf("got %+v, want %+v", scan, want)
	}
}

// A token past the first window of a long line, as in minified code, is
// found; a binary file is not scanned.
func TestScanSecretsLongLineAndBinary(t *testing.T) {
	ghp := token("gh"+"p_", 36, alnum)
	long := strings.Repeat("a ", scanWindow) + ghp + strings.Repeat(" b", scanWindow) + "\nnext\n"
	scan, err := ScanSecrets("app.min.js", strings.NewReader(long), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Found) != 1 || scan.Found[0].Line != 1 {
		t.Fatalf("got %+v, want line 1", scan.Found)
	}
	scan, err = ScanSecrets("logo.png", strings.NewReader("\x89PNG\x00\n"+ghp+"\n"), nil)
	if err != nil || len(scan.Found) != 0 {
		t.Fatalf("got %+v, %v; want nothing for a binary file", scan, err)
	}
}

// #1602: a license and a README, non-empty and at the root, under each of
// their names.
func TestPublishChecks(t *testing.T) {
	readme := CheckedFile{Path: "README.md", Size: 10}
	license := CheckedFile{Path: "LICENSE", Size: 10}
	secret := func(path string, line int) CheckedFile {
		return CheckedFile{Path: path, Size: 10, Secrets: SecretScan{Found: []PublishProblem{
			{Reason: PublishSecretFound, Path: path, Line: line, Rule: "a private key"}}}}
	}
	cases := []struct {
		name  string
		files []CheckedFile
		want  []string
	}{
		{"all there", []CheckedFile{license, readme}, nil},
		{"LICENSE.md", []CheckedFile{{Path: "LICENSE.md", Size: 1}, readme}, nil},
		{"LICENSE.txt", []CheckedFile{{Path: "LICENSE.txt", Size: 1}, readme}, nil},
		{"COPYING", []CheckedFile{{Path: "COPYING", Size: 1}, readme}, nil},
		{"README", []CheckedFile{license, {Path: "README", Size: 1}}, nil},
		{"no license", []CheckedFile{readme}, []string{PublishLicenseMissing}},
		{"empty license", []CheckedFile{{Path: "LICENSE", Size: 0}, readme}, []string{PublishLicenseMissing}},
		{"license in a subfolder", []CheckedFile{{Path: "docs/LICENSE", Size: 9}, readme},
			[]string{PublishLicenseMissing}},
		{"no readme", []CheckedFile{license}, []string{PublishReadmeMissing}},
		{"empty readme", []CheckedFile{license, {Path: "README.md"}}, []string{PublishReadmeMissing}},
		{"readme in a subfolder", []CheckedFile{license, {Path: "web/README.md", Size: 3}},
			[]string{PublishReadmeMissing}},
		{"nothing", nil, []string{PublishLicenseMissing, PublishReadmeMissing}},
		{"secrets first, by path and line", []CheckedFile{secret("b.txt", 1), secret("a.txt", 7), readme},
			[]string{PublishSecretFound, PublishSecretFound, PublishLicenseMissing}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, p := range PublishChecks(c.files) {
				got = append(got, p.Reason)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
	got := PublishProblemsMessage(PublishChecks([]CheckedFile{secret("b.txt", 1), secret("a.txt", 7)}), 0)
	want := "a.txt line 7: a private key; b.txt line 1: a private key; " +
		"no license: add a non-empty LICENSE, LICENSE.md, LICENSE.txt or COPYING at the root; " +
		"no README: add a non-empty README.md or README at the root"
	if got != want {
		t.Errorf("message\n%s\nwant\n%s", got, want)
	}
}

// #1602: lines the mark skipped are said when no secret is found.
func TestPublishProblemsMessageAllowed(t *testing.T) {
	got := PublishProblemsMessage([]PublishProblem{{Reason: PublishReadmeMissing}}, 2)
	if !strings.HasSuffix(got, "(2 lines marked c4factory:allow-secret were skipped)") {
		t.Errorf("message %q doesn't say the lines skipped", got)
	}
	if got := PublishProblemsMessage([]PublishProblem{{Reason: PublishSecretFound, Path: "a", Line: 1,
		Rule: "a private key"}}, 2); strings.Contains(got, "skipped") {
		t.Errorf("message %q says the skipped lines beside a secret", got)
	}
}

// Check leaves out the files not published, the factory's own.
func TestReleaseSourceCheck(t *testing.T) {
	s := &ReleaseSource{Files: []SourceFile{{Path: "LICENSE"}, {Path: "README.md"}}}
	s.Check([]CheckedFile{{Path: "LICENSE", Size: 1}, {Path: "README.md", Size: 1},
		{Path: ".gitea/workflows/ci.yml", Size: 1, Secrets: SecretScan{Allowed: 1, Found: []PublishProblem{
			{Reason: PublishSecretFound, Path: ".gitea/workflows/ci.yml", Line: 1, Rule: "a private key"}}}}})
	if len(s.Problems) != 0 || s.Allowed != 0 {
		t.Fatalf("got %v, %d; want the factory's own file left out", s.Problems, s.Allowed)
	}
}

// #1602: a source with a problem fails before any write.
func TestNextSourceStepProblems(t *testing.T) {
	files := &ReleaseSource{Files: []SourceFile{{Path: "a"}}, Tree: "abc",
		Problems: []PublishProblem{{Reason: PublishSecretFound, Path: "a", Line: 3, Rule: "a private key"},
			{Reason: PublishLicenseMissing}}}
	step, done := NextSourceStep(SourceShip{Source: PublishSource{Path: "."}, Files: files, Tag: "v1.0.0"},
		&GitHubSource{Head: "h", HeadTree: "t"})
	if done != nil || step.Action != PublishFail || step.Reason != PublishSecretFound ||
		!strings.Contains(step.Message, "a line 3: a private key; no license") {
		t.Fatalf("got %+v", step)
	}
}

// ScanBlob hashes what it scans, as HashBlob does.
func TestScanBlob(t *testing.T) {
	content := "package engine\n"
	blob, scan, err := ScanBlob("engine.go", strings.NewReader(content), int64(len(content)), nil)
	if err != nil || blob != engineGo || len(scan.Found) != 0 {
		t.Fatalf("got %s, %+v, %v", blob, scan, err)
	}
	if _, _, err := ScanBlob("engine.go", strings.NewReader(content), 99, nil); err == nil {
		t.Fatal("a short read passed")
	}
}
