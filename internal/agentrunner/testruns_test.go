package agentrunner

import (
	"strings"
	"testing"
)

// A run that leased a test-run environment, such as Device Farm's phones,
// is told to run its test packages there with theos-env test, not commands
// with theos-env run (docs/plans/done/test-environments.md, T12, step 10).
func TestPromptsListTestRunEnvironments(t *testing.T) {
	job, err := JobFromEnv(env(map[string]string{EnvEnvironments: `[{"name":"devices",` +
		`"description":"Pixel 8 and Galaxy S24 on AWS Device Farm","testRuns":true}]`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Environments) != 1 || !job.Environments[0].TestRuns {
		t.Fatalf("Environments = %+v, want devices, which runs test packages", job.Environments)
	}
	for name, prompt := range map[string]string{"implement": ImplementPrompt(job, Workspace{}),
		"verify": VerifyPrompt(job, Workspace{})} {
		for _, want := range []string{"- `devices`: Pixel 8 and Galaxy S24 on AWS Device Farm (runs test packages",
			"`theos-env test devices --type <instrumentation|xctest|xctest-ui> --app <file> --tests <file>`",
			"test-runs/devices/", "comments the outcome on the pull request"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s prompt lacks %q:\n%s", name, want, prompt)
			}
		}
		if strings.Contains(prompt, "theos-env run devices") {
			t.Errorf("%s prompt tells the run to run commands on a test-run environment", name)
		}
	}

	both, err := JobFromEnv(env(map[string]string{EnvEnvironments: `[{"name":"android"},` +
		`{"name":"devices","testRuns":true}]`}))
	if err != nil {
		t.Fatal(err)
	}
	prompt := ImplementPrompt(both, Workspace{})
	for _, want := range []string{"`theos-env run android -- <command>`", "`theos-env test devices --type"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("a run with both kinds lacks %q", want)
		}
	}
}
