package model

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestResolveOperations(t *testing.T) {
	days := func(n int32) *int32 { return &n }
	defaults := Operations{
		Backups:  OperationsBackups{RetentionDays: 35},
		Recovery: OperationsRecovery{RPO: "1h", RTO: "4h"},
	}
	for _, tt := range []struct {
		name string
		in   OperationsChoices
		want Operations
	}{
		{"left out", OperationsChoices{}, defaults},
		{"zero retention is point-in-time recovery only", OperationsChoices{RetentionDays: days(0)},
			Operations{Backups: OperationsBackups{}, Recovery: defaults.Recovery}},
		{"one field keeps the other defaults", OperationsChoices{RPO: "15m"},
			Operations{Backups: defaults.Backups, Recovery: OperationsRecovery{RPO: "15m", RTO: "4h"}}},
		{"every field", OperationsChoices{RetentionDays: days(90), CopyToRegion: "eu-west-1", RPO: "5m", RTO: "1h",
			Email: "ops@example.com"}, Operations{
			Backups:  OperationsBackups{RetentionDays: 90, CopyToRegion: "eu-west-1"},
			Recovery: OperationsRecovery{RPO: "5m", RTO: "1h"},
			Alerts:   OperationsAlerts{Email: "ops@example.com"},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveOperations(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ResolveOperations(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestOperationsProblems(t *testing.T) {
	days := func(n int32) *int32 { return &n }
	for _, tt := range []struct {
		name string
		in   OperationsChoices
		want []string
	}{
		{"left out", OperationsChoices{}, nil},
		{"valid", OperationsChoices{RetentionDays: days(3650), CopyToRegion: "us-gov-west-1", RPO: "30m", RTO: "2d",
			Email: "a@b.io"}, nil},
		{"negative retention", OperationsChoices{RetentionDays: days(-1)}, []string{"backups.retentionDays"}},
		{"retention too long", OperationsChoices{RetentionDays: days(3651)}, []string{"backups.retentionDays"}},
		{"region", OperationsChoices{CopyToRegion: "Europe"}, []string{"backups.copyToRegion"}},
		{"objectives", OperationsChoices{RPO: "1 hour", RTO: "4w"}, []string{"recovery.rpo", "recovery.rto"}},
		{"email", OperationsChoices{Email: "ops team"}, []string{"alerts.email"}},
		// Values the patterns accept but the CRD's MaxLength markers refuse (#1210).
		{"rpo too long", OperationsChoices{RPO: "00000000000000001h"}, []string{"recovery.rpo"}},
		{"rto too long", OperationsChoices{RTO: strings.Repeat("0", MaxObjectiveLength) + "d"},
			[]string{"recovery.rto"}},
		{"region too long", OperationsChoices{CopyToRegion: "eu-" + strings.Repeat("w", MaxCopyToRegionLength) + "-1"},
			[]string{"backups.copyToRegion"}},
		{"email too long", OperationsChoices{Email: strings.Repeat("o", MaxEmailLength) + "@b.io"},
			[]string{"alerts.email"}},
		{"longest values", OperationsChoices{
			CopyToRegion: "eu-" + strings.Repeat("w", MaxCopyToRegionLength-5) + "-1",
			RPO:          strings.Repeat("1", MaxObjectiveLength-1) + "m",
			RTO:          strings.Repeat("1", MaxObjectiveLength-1) + "d",
			Email:        strings.Repeat("o", MaxEmailLength-5) + "@b.io",
		}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := OperationsProblems(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("OperationsProblems(%+v) = %q, want one per %q", tt.in, got, tt.want)
			}
			for i, field := range tt.want {
				if !strings.HasPrefix(got[i], field+" ") {
					t.Errorf("problem %d = %q, want it to name %s", i, got[i], field)
				}
			}
		})
	}
}

// TestOperationsPatternsMatchTheCRD holds OperationsProblems' patterns,
// lengths and range to the Container CRD's markers, so the plan check refuses
// what the API server would (#1210).
func TestOperationsPatternsMatchTheCRD(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "theos.factory.io_containers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	type field struct {
		Pattern    string           `yaml:"pattern"`
		MaxLength  *int             `yaml:"maxLength"`
		Minimum    *int32           `yaml:"minimum"`
		Maximum    *int32           `yaml:"maximum"`
		Properties map[string]field `yaml:"properties"`
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema field `yaml:"openAPIV3Schema"`
				} `yaml:"schema"`
			} `yaml:"versions"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatal(err)
	}
	ops := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["operations"].Properties
	for name, want := range map[string]struct {
		pattern   string
		maxLength int
	}{
		"backups.copyToRegion": {regionPattern.String(), MaxCopyToRegionLength},
		"recovery.rpo":         {objectivePattern.String(), MaxObjectiveLength},
		"recovery.rto":         {objectivePattern.String(), MaxObjectiveLength},
		"alerts.email":         {emailPattern.String(), MaxEmailLength},
	} {
		group, f, _ := strings.Cut(name, ".")
		got := ops[group].Properties[f]
		if got.Pattern != want.pattern {
			t.Errorf("%s: the CRD's pattern is %q, OperationsProblems' %q", name, got.Pattern, want.pattern)
		}
		if got.MaxLength == nil || *got.MaxLength != want.maxLength {
			t.Errorf("%s: the CRD's maxLength is %v, OperationsProblems' %d", name, got.MaxLength, want.maxLength)
		}
	}
	days := ops["backups"].Properties["retentionDays"]
	if days.Minimum == nil || *days.Minimum != 0 || days.Maximum == nil || *days.Maximum != MaxRetentionDays {
		t.Errorf("backups.retentionDays: the CRD's range is %v..%v, OperationsProblems' 0..%d",
			days.Minimum, days.Maximum, MaxRetentionDays)
	}
}

// An RPO of 0 is valid, and the Standard reads it as under a day (#1266).
func TestZeroRPO(t *testing.T) {
	for rpo, want := range map[string]bool{
		"0m": true, "0h": true, "0d": true, "00m": true,
		"1h": false, "10m": false, "05m": false, "": false, "0": false, "m": false,
	} {
		if got := (OperationsRecovery{RPO: rpo}).ZeroRPO(); got != want {
			t.Errorf("ZeroRPO(%q) = %t, want %t", rpo, got, want)
		}
	}
}
