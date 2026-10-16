package scaffold

import (
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/c4factory/internal/model"
)

// The AWS Frameworks render the Container's spec.operations into
// infra/operations.ts, and aws-standard its runbook from it
// (plans/aws-production.md, decisions 1 and 3).

// awsLambdaFrameworks are the AWS Frameworks with spec.operations: each
// renders infra/operations.ts, and aws-standard a runbook for it (steps 3 and
// 4 of plans/aws-production.md). The others come with their own steps.
var awsLambdaFrameworks = []string{"aws-lambda-ts", "aws-lambda-py", "aws-lambda-go"}

// renderOperations renders framework with aws-standard for a Container whose
// spec.operations is c.
func renderOperations(t *testing.T, typ, framework string, c model.OperationsChoices) Result {
	t.Helper()
	in, layers := loadCombo(t, typ, []side{
		{model.SideFramework, framework},
		{model.SideStandard, "aws-standard"},
	}, "")
	in.Operations = model.ResolveOperations(c)
	result, err := Build(in, layers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAWSLambdaFrameworksRenderTheOperationsBlock(t *testing.T) {
	week := int32(7)
	tests := []struct {
		name   string
		block  model.OperationsChoices
		wanted []string
	}{
		{"the defaults", model.OperationsChoices{}, []string{
			"retentionDays: 35,", "copyToRegion: '',", "rpo: '1h',", "rto: '4h',", "email: '',",
		}},
		{"every field set", model.OperationsChoices{
			RetentionDays: &week, CopyToRegion: "eu-west-1", RPO: "15m", RTO: "1d", Email: "ops@example.com",
		}, []string{
			"retentionDays: 7,", "copyToRegion: 'eu-west-1',", "rpo: '15m',", "rto: '1d',",
			"email: 'ops@example.com',",
		}},
		// Prettier quotes a string holding a single quote with double ones.
		{"an email with a quote", model.OperationsChoices{Email: "o'neil@example.com"}, []string{
			`email: "o'neil@example.com",`,
		}},
	}
	for _, framework := range awsLambdaFrameworks {
		for _, tt := range tests {
			t.Run(framework+"/"+tt.name, func(t *testing.T) {
				r := renderOperations(t, model.TypeFunction, framework, tt.block)
				f, ok := r.Files["infra/operations.ts"]
				if !ok {
					t.Fatalf("%s writes no infra/operations.ts", framework)
				}
				for _, w := range tt.wanted {
					if !strings.Contains(string(f.Data), w) {
						t.Errorf("infra/operations.ts lacks %q:\n%s", w, f.Data)
					}
				}
			})
		}
	}
}

func TestAWSStandardWritesTheRunbookForTheFrameworksWithOperations(t *testing.T) {
	for _, f := range awsFrameworks {
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{
				CopyToRegion: "eu-west-1", RPO: "15m", RTO: "2h", Email: "ops@example.com",
			})
			runbook, ok := r.Files["docs/runbook.md"]
			if !slices.Contains(awsLambdaFrameworks, f.Name) {
				// The other Frameworks' branches come with their own steps.
				if ok {
					t.Errorf("aws-standard writes docs/runbook.md for %s, which has no operations yet", f.Name)
				}
				return
			}
			if !ok {
				t.Fatalf("aws-standard writes no docs/runbook.md for %s", f.Name)
			}
			doc := string(runbook.Data)
			for _, w := range []string{
				"(RPO): 15m", "(RTO): 2h", "kept 35 days, each copied to eu-west-1", "ops@example.com",
				"restore-table-to-point-in-time", "continue-update-rollback", "GuardDuty",
				"Api-Fault-Count-Critical", "| `Items` table",
			} {
				if !strings.Contains(doc, w) {
					t.Errorf("docs/runbook.md lacks %q", w)
				}
			}
			// Powertools' idempotency records live in a table of their own;
			// Go has no Powertools, so its stack has no such table.
			idempotency := f.Name != "aws-lambda-go"
			for _, w := range []string{"| `Idempotency` table", "`Idempotency-...`", "for the `Idempotency` table"} {
				if strings.Contains(doc, w) != idempotency {
					t.Errorf("docs/runbook.md for %s says %q: %t, want %t", f.Name, w, !idempotency, idempotency)
				}
			}
		})
	}
}

func TestAWSStandardRunbookSaysWhenThereAreNoBackups(t *testing.T) {
	none := int32(0)
	for _, framework := range awsLambdaFrameworks {
		t.Run(framework, func(t *testing.T) {
			r := renderOperations(t, model.TypeFunction, framework, model.OperationsChoices{RetentionDays: &none})
			runbook := string(r.Files["docs/runbook.md"].Data)
			if !strings.Contains(runbook, "point-in-time recovery only") {
				t.Error("docs/runbook.md doesn't say that retentionDays 0 keeps point-in-time recovery only")
			}
			for _, never := range []string{
				"## Restore from a backup in the vault", "The daily backups", "backup vault and key",
			} {
				if strings.Contains(runbook, never) {
					t.Errorf("docs/runbook.md says %q with no backups", never)
				}
			}
		})
	}
}

// TestAWSStandardGuidesEveryLambdaFrameworkThroughTheTiers checks that every
// file of aws-standard that says how to work with the tiers says it for the
// three Lambda Frameworks, whose own branches (aws-lambda-go's notes) are
// easy to miss, and for no Framework without infra/operations.ts yet.
func TestAWSStandardGuidesEveryLambdaFrameworkThroughTheTiers(t *testing.T) {
	says := map[string]string{
		"AGENTS.md":                  "`infra/operations.ts` is the factory's",
		"docs/agents/implementer.md": "The stacks take the tier from `defineStacks(app, tier)`",
		"docs/agents/reviewer.md":    "a resource that ignores the tier",
		"docs/style/aws.md":          "Build it with the\n  tier's `TIER_RULES`",
		"docs/style/SOURCES.md":      "## Operations (read 2026-10-16)",
		"docs/well-architected.md":   "## Tiers and the operations block",
	}
	for _, f := range awsFrameworks {
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{})
			lambda := slices.Contains(awsLambdaFrameworks, f.Name)
			for path, w := range says {
				file, ok := r.Files[path]
				if !ok {
					t.Fatalf("aws-standard writes no %s for %s", path, f.Name)
				}
				if strings.Contains(string(file.Data), w) != lambda {
					t.Errorf("%s for %s says %q: %t, want %t", path, f.Name, w, !lambda, lambda)
				}
			}
		})
	}
}
