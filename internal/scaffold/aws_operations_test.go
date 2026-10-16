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

func TestAWSLambdaTSRendersTheOperationsBlock(t *testing.T) {
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := renderOperations(t, model.TypeFunction, "aws-lambda-ts", tt.block)
			f, ok := r.Files["infra/operations.ts"]
			if !ok {
				t.Fatal("aws-lambda-ts writes no infra/operations.ts")
			}
			for _, w := range tt.wanted {
				if !strings.Contains(string(f.Data), w) {
					t.Errorf("infra/operations.ts lacks %q:\n%s", w, f.Data)
				}
			}
		})
	}
}

// runbookSays is what each Framework with operations has its runbook say for
// a Container whose block sets every field but retentionDays (35, the
// default): the block's values, and the steps only that Framework has.
var runbookSays = map[string][]string{
	"aws-lambda-ts": {
		"(RPO): 15m", "(RTO): 2h", "kept 35 days, each copied to eu-west-1", "ops@example.com",
		"restore-table-to-point-in-time", "continue-update-rollback", "GuardDuty",
		"Api-Fault-Count-Critical",
	},
	"aws-static-site-ts": {
		"(RPO): 15m", "(RTO): 2h", "eu-west-1", "ops@example.com", "35 days",
		"list-object-versions", "create-invalidation", "continue-update-rollback", "GuardDuty",
		"Site-Fault-Rate-Critical", "Deploy-Fault-Count-Critical",
	},
}

func TestAWSStandardWritesTheRunbookForTheFrameworksWithOperations(t *testing.T) {
	for _, f := range awsFrameworks {
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{
				CopyToRegion: "eu-west-1", RPO: "15m", RTO: "2h", Email: "ops@example.com",
			})
			runbook, ok := r.Files["docs/runbook.md"]
			wanted, has := runbookSays[f.Name]
			if !has {
				// The other Frameworks' branches come with their own steps.
				if ok {
					t.Errorf("aws-standard writes docs/runbook.md for %s, which has no operations yet", f.Name)
				}
				return
			}
			if !ok {
				t.Fatalf("aws-standard writes no docs/runbook.md for %s", f.Name)
			}
			for _, w := range wanted {
				if !strings.Contains(string(runbook.Data), w) {
					t.Errorf("docs/runbook.md lacks %q", w)
				}
			}
			if _, ok := r.Files["infra/operations.ts"]; !ok {
				t.Errorf("%s writes no infra/operations.ts", f.Name)
			}
		})
	}
}

// The static site keeps its files' earlier versions where a table would be
// backed up: the runbook says for how long, and that there is no backup plan.
func TestAWSStandardRunbookKeepsTheSitesVersions(t *testing.T) {
	for _, tt := range []struct {
		days int32
		want string
	}{{0, "35 days"}, {35, "35 days"}, {90, "90 days"}} {
		r := renderOperations(t, model.TypeStaticSite, "aws-static-site-ts", model.OperationsChoices{RetentionDays: &tt.days})
		runbook := string(r.Files["docs/runbook.md"].Data)
		if !strings.Contains(runbook, "earlier versions for "+tt.want) {
			t.Errorf("retentionDays %d: docs/runbook.md doesn't say it keeps earlier versions for %s", tt.days, tt.want)
		}
		if strings.Contains(runbook, "## Restore from a backup in the vault") {
			t.Errorf("retentionDays %d: docs/runbook.md restores from a vault the site has none of", tt.days)
		}
	}
}

func TestAWSStandardRunbookSaysWhenThereAreNoBackups(t *testing.T) {
	none := int32(0)
	r := renderOperations(t, model.TypeFunction, "aws-lambda-ts", model.OperationsChoices{RetentionDays: &none})
	runbook := string(r.Files["docs/runbook.md"].Data)
	if !strings.Contains(runbook, "point-in-time recovery only") {
		t.Error("docs/runbook.md doesn't say that retentionDays 0 keeps point-in-time recovery only")
	}
	for _, never := range []string{"## Restore from a backup in the vault", "The daily backups"} {
		if strings.Contains(runbook, never) {
			t.Errorf("docs/runbook.md says %q with no backups", never)
		}
	}
}
