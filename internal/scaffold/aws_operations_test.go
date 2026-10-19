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
// the alarm its runbook branch must name and a line only that branch has,
// whether it keeps its data as a bucket's versions instead of in a backup
// plan, and whether its stack keeps Powertools' idempotency records in a table
// of their own (Go has no Powertools, so its stack has no such table).
var awsOperationsFrameworks = []struct {
	side
	runbook     []string
	versions    bool
	idempotency bool
}{
	{side{model.TypeFunction, "aws-lambda-ts"}, []string{"Api-Fault-Count-Critical", "| `Items` table"}, false, true},
	{side{model.TypeFunction, "aws-lambda-py"}, []string{"Api-Fault-Count-Critical", "| `Items` table"}, false, true},
	{side{model.TypeFunction, "aws-lambda-go"}, []string{"Api-Fault-Count-Critical", "| `Items` table"}, false, false},
	{side{model.TypeService, "aws-ec2-ts"}, []string{
		"LoadBalancer-5xx-Critical", "Service-Unhealthy-Instances-Critical", "Instances-In-Service-Critical",
		"resource signal", "never backed up", "CertificateArn", "terminate-instance-in-auto-scaling-group",
	}, false, false},
	{side{model.TypeService, "aws-fargate-ts"}, []string{
		"Service-Unhealthy-Tasks-Critical", "LoadBalancer-5xx-Rate-Critical", "Service-Running-Tasks-Critical",
		"rolloutState", "circuit breaker", "in each of its two zones", "CollectorLogs",
	}, false, false},
	{side{model.TypeStaticSite, "aws-static-site-ts"}, []string{
		"Site-Fault-Rate-Critical", "Deploy-Fault-Count-Critical", "list-object-versions", "create-invalidation",
	}, true, false},
	{side{model.TypeWorker, "aws-queue-ts"}, []string{
		"Messages-DLQ-Queue-Message-Count-Max-Critical", "Messages-Queue-Message-Age-Max-Warning",
		"aws sqs start-message-move-task", "`DeadLetters` queue", "the longest the worker may be",
	}, false, true},
	{side{model.TypeJob, "aws-schedule-ts"}, []string{
		"Schedule-InvocationDroppedCount-Critical", "DeadLetters-Queue-Message-Count-Max-Critical",
		"aws lambda invoke", "`DeadLetters` queue",
	}, false, true},
	{side{model.TypeWorker, "aws-event-ts"}, []string{
		"Rule-FailedInvocations-Critical", "Rule-InvocationsFailedToBeSentToDlq-Critical",
		"DeadLetters-Queue-Message-Count-Max-Critical", "aws lambda invoke", "aws events disable-rule",
		"`DeadLetters` queue",
	}, false, true},
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

// runbookSays is what each Framework with operations has its runbook say for
// a Container whose block sets every field but retentionDays (35, the
// default): the block's values and the steps its kind of data shares;
// awsOperationsFrameworks adds the lines only its own branch has.
var runbookSays = map[string][]string{
	"aws-lambda-ts":   tableRunbookSays,
	"aws-lambda-py":   tableRunbookSays,
	"aws-lambda-go":   tableRunbookSays,
	"aws-ec2-ts":      tableRunbookSays,
	"aws-fargate-ts":  tableRunbookSays,
	"aws-queue-ts":    tableRunbookSays,
	"aws-schedule-ts": tableRunbookSays,
	"aws-event-ts":    tableRunbookSays,
	"aws-static-site-ts": {
		"(RPO): 15m", "(RTO): 2h", "eu-west-1", "ops@example.com", "35 days",
		"continue-update-rollback", "GuardDuty",
	},
}

// tableRunbookSays is what the runbook of a Framework keeping a table says.
var tableRunbookSays = []string{
	"(RPO): 15m", "(RTO): 2h", "kept 35 days, each copied to eu-west-1", "ops@example.com",
	"restore-table-to-point-in-time", "continue-update-rollback", "GuardDuty",
	"-backup-copy",
}

func TestAWSStandardWritesTheRunbookForTheFrameworksWithOperations(t *testing.T) {
	for _, f := range awsFrameworks {
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{
				CopyToRegion: "eu-west-1", RPO: "15m", RTO: "2h", Email: "ops@example.com",
			})
			runbook, ok := r.Files["docs/runbook.md"]
			wanted, has := runbookSays[f.Name]
			if has != hasOperations(f.Name) {
				t.Fatalf("runbookSays and awsOperationsFrameworks disagree on %s", f.Name)
			}
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
			var own []string
			idempotency := false
			for _, o := range awsOperationsFrameworks {
				if o.Name == f.Name {
					own, idempotency = o.runbook, o.idempotency
				}
			}
			doc := string(runbook.Data)
			for _, w := range append(append([]string{}, wanted...), own...) {
				if !strings.Contains(doc, w) {
					t.Errorf("docs/runbook.md lacks %q", w)
				}
			}
			for _, w := range []string{"| `Idempotency` table", "`Idempotency-...`", "for the `Idempotency` table"} {
				if strings.Contains(doc, w) != idempotency {
					t.Errorf("docs/runbook.md for %s says %q: %t, want %t", f.Name, w, !idempotency, idempotency)
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
	for _, f := range awsOperationsFrameworks {
		if f.versions {
			continue // TestAWSStandardRunbookKeepsTheSitesVersions
		}
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{RetentionDays: &none})
			runbook := string(r.Files["docs/runbook.md"].Data)
			if !strings.Contains(runbook, "point-in-time recovery only") {
				t.Error("docs/runbook.md doesn't say that retentionDays 0 keeps point-in-time recovery only")
			}
			for _, never := range []string{
				"## Restore from a backup in the vault", "The daily backups", "backup vault and key",
				"AWS Backup's storage", "per vault",
			} {
				if strings.Contains(runbook, never) {
					t.Errorf("docs/runbook.md says %q with no backups", never)
				}
			}
		})
	}
}

// TestAWSStandardGuidesEveryFrameworkWithOperationsThroughTheTiers checks that
// every file of aws-standard that says how to work with the tiers says it for
// each Framework with operations (the three Lambda Frameworks, aws-fargate-ts
// and aws-queue-ts), whose own branches (aws-lambda-go's notes, aws-fargate-ts's
// digests) are easy to miss, and for no Framework without infra/operations.ts
// yet. aws-static-site-ts, which keeps its data as a bucket's versions, has
// whole files of its own that say it in their own words.
func TestAWSStandardGuidesEveryFrameworkWithOperationsThroughTheTiers(t *testing.T) {
	says := map[string]string{
		"AGENTS.md":                  "`infra/operations.ts` is the factory's",
		"docs/agents/implementer.md": "The stacks take the tier from `defineStacks(app, tier)`",
		"docs/agents/reviewer.md":    "a resource that ignores the tier",
		"docs/style/aws.md":          "Build it with the\n  tier's `TIER_RULES`",
		"docs/style/SOURCES.md":      "## Operations (read 2026-10-16)",
		"docs/well-architected.md":   "## Tiers and the operations block",
	}
	site := map[string]bool{}
	for _, o := range awsOperationsFrameworks {
		site[o.Name] = o.versions
	}
	for _, f := range awsFrameworks {
		if site[f.Name] {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			r := renderOperations(t, f.Side, f.Name, model.OperationsChoices{})
			tiered := hasOperations(f.Name)
			for path, w := range says {
				file, ok := r.Files[path]
				if !ok {
					t.Fatalf("aws-standard writes no %s for %s", path, f.Name)
				}
				if strings.Contains(string(file.Data), w) != tiered {
					t.Errorf("%s for %s says %q: %t, want %t", path, f.Name, w, !tiered, tiered)
				}
			}
		})
	}
}
