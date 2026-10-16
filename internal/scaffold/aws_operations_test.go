package scaffold

import (
	"strings"
	"testing"

	"github.com/JairJosafath/c4factory/internal/model"
)

// The AWS Frameworks render the Container's spec.operations into
// infra/operations.ts, and aws-standard its runbook from it
// (plans/aws-production.md, decisions 1 and 3).

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

// awsOperationsFrameworks is every AWS Framework that renders the block, with
// the alarm its runbook branch must name and a line only that branch has.
var awsOperationsFrameworks = []struct {
	side
	runbook []string
}{
	{side{model.TypeFunction, "aws-lambda-ts"}, []string{"Api-Fault-Count-Critical", "Idempotency"}},
	{side{model.TypeService, "aws-fargate-ts"}, []string{
		"Service-Unhealthy-Tasks-Critical", "LoadBalancer-5xx-Rate-Critical", "Service-Running-Tasks-Critical",
		"rolloutState", "circuit breaker", "in each of its two zones", "CollectorLogs",
	}},
	{side{model.TypeWorker, "aws-queue-ts"}, []string{
		"Messages-DLQ-Queue-Message-Count-Max-Critical", "Messages-Queue-Message-Age-Max-Warning",
		"aws sqs start-message-move-task", "`DeadLetters` queue", "the longest the worker may be",
	}},
}

// hasOperations reports whether framework renders infra/operations.ts.
func hasOperations(framework string) bool {
	for _, f := range awsOperationsFrameworks {
		if f.Name == framework {
			return true
		}
	}
	return false
}

func TestAWSFrameworksRenderTheOperationsBlock(t *testing.T) {
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
	for _, fw := range awsOperationsFrameworks {
		for _, tt := range tests {
			t.Run(fw.Name+"/"+tt.name, func(t *testing.T) {
				r := renderOperations(t, fw.Side, fw.Name, tt.block)
				f, ok := r.Files["infra/operations.ts"]
				if !ok {
					t.Fatalf("%s writes no infra/operations.ts", fw.Name)
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

func TestAWSFrameworksWithoutOperationsRenderNoBlock(t *testing.T) {
	for _, f := range awsFrameworks {
		if hasOperations(f.Name) {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{})
			if _, ok := r.Files["infra/operations.ts"]; ok {
				t.Errorf("%s writes infra/operations.ts before its step of plans/aws-production.md", f.Name)
			}
		})
	}
}

func TestAWSStandardWritesTheRunbookForTheFrameworksWithOperations(t *testing.T) {
	for _, f := range awsFrameworks {
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{
				CopyToRegion: "eu-west-1", RPO: "15m", RTO: "2h", Email: "ops@example.com",
			})
			runbook, ok := r.Files["docs/runbook.md"]
			var own []string
			for _, o := range awsOperationsFrameworks {
				if o.Name == f.Name {
					own = o.runbook
				}
			}
			if !hasOperations(f.Name) {
				// The other Frameworks' branches come with their own steps.
				if ok {
					t.Errorf("aws-standard writes docs/runbook.md for %s, which has no operations yet", f.Name)
				}
				return
			}
			if !ok {
				t.Fatalf("aws-standard writes no docs/runbook.md for %s", f.Name)
			}
			for _, w := range append([]string{
				"(RPO): 15m", "(RTO): 2h", "kept 35 days, each copied to eu-west-1", "ops@example.com",
				"restore-table-to-point-in-time", "continue-update-rollback", "GuardDuty",
				"-backup-copy",
			}, own...) {
				if !strings.Contains(string(runbook.Data), w) {
					t.Errorf("docs/runbook.md lacks %q", w)
				}
			}
			// One Framework's alarms never reach another's runbook.
			for _, other := range awsOperationsFrameworks {
				if other.Name != f.Name && strings.Contains(string(runbook.Data), other.runbook[0]) {
					t.Errorf("docs/runbook.md for %s says %q, %s's", f.Name, other.runbook[0], other.Name)
				}
			}
		})
	}
}

func TestAWSStandardRunbookSaysWhenThereAreNoBackups(t *testing.T) {
	none := int32(0)
	for _, f := range awsOperationsFrameworks {
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{RetentionDays: &none})
			runbook := string(r.Files["docs/runbook.md"].Data)
			if !strings.Contains(runbook, "point-in-time recovery only") {
				t.Error("docs/runbook.md doesn't say that retentionDays 0 keeps point-in-time recovery only")
			}
			for _, never := range []string{
				"## Restore from a backup in the vault", "The daily backups", "AWS Backup's storage", "per vault",
			} {
				if strings.Contains(runbook, never) {
					t.Errorf("docs/runbook.md says %q with no backups", never)
				}
			}
		})
	}
}
