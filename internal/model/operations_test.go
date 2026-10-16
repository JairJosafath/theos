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

// TestOperationsPatternsMatchTheCRD holds OperationsProblems' patterns and
// range to the Container CRD's markers, so the plan check refuses what the
// API server would.
func TestOperationsPatternsMatchTheCRD(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "c4.factory.io_containers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	type field struct {
		Pattern    string           `yaml:"pattern"`
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
	for name, want := range map[string]string{
		"backups.copyToRegion": regionPattern.String(),
		"recovery.rpo":         objectivePattern.String(),
		"recovery.rto":         objectivePattern.String(),
		"alerts.email":         emailPattern.String(),
	} {
		group, f, _ := strings.Cut(name, ".")
		if got := ops[group].Properties[f].Pattern; got != want {
			t.Errorf("%s: the CRD's pattern is %q, OperationsProblems' %q", name, got, want)
		}
	}
	days := ops["backups"].Properties["retentionDays"]
	if days.Minimum == nil || *days.Minimum != 0 || days.Maximum == nil || *days.Maximum != MaxRetentionDays {
		t.Errorf("backups.retentionDays: the CRD's range is %v..%v, OperationsProblems' 0..%d",
			days.Minimum, days.Maximum, MaxRetentionDays)
	}
}
