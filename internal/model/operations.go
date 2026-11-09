/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A Container's spec.operations is what the app promises whatever tier runs
// it (plans/aws-production.md, decision 1). The factory fills in what the
// block leaves out here, never by CRD defaulting, so a client reads back only
// what it set and a stored object keeps its shape on an upgrade. The tier is
// the deploy target's and has no place in the block.

// The defaults of a field the block leaves out.
const (
	DefaultRetentionDays int32 = 35
	DefaultRPO                 = "1h"
	DefaultRTO                 = "4h"
	// MaxRetentionDays is the most days backups may be kept.
	MaxRetentionDays int32 = 3650
)

// The MaxLength markers of the CRD's OperationsSpec, in characters; the test
// that holds the patterns to the CRD holds these too.
const (
	MaxCopyToRegionLength = 32
	MaxObjectiveLength    = 16
	MaxEmailLength        = 254
)

// OperationsChoices is a Container's spec.operations as written: a nil
// RetentionDays, or an empty string, is a field left out.
type OperationsChoices struct {
	RetentionDays *int32
	CopyToRegion  string
	RPO           string
	RTO           string
	Email         string
}

// Operations is a Container's operations block with the defaults filled in:
// what the templates render infra/operations.ts from.
type Operations struct {
	Backups  OperationsBackups
	Recovery OperationsRecovery
	Alerts   OperationsAlerts
}

// OperationsBackups: RetentionDays 0 means point-in-time recovery only, and
// an empty CopyToRegion no copy.
type OperationsBackups struct {
	RetentionDays int32
	CopyToRegion  string
}

// OperationsRecovery holds the recovery objectives, such as 1h.
type OperationsRecovery struct {
	RPO string
	RTO string
}

// ZeroRPO reports whether the RPO is zero minutes, hours or days, which the
// API accepts and asks to lose nothing; aws-standard reads it as under a day
// (#1266).
func (r OperationsRecovery) ZeroRPO() bool {
	return objectivePattern.MatchString(r.RPO) && strings.Trim(r.RPO[:len(r.RPO)-1], "0") == ""
}

// OperationsAlerts: an empty Email tells nobody.
type OperationsAlerts struct {
	Email string
}

// ResolveOperations fills in the defaults of what c leaves out.
func ResolveOperations(c OperationsChoices) Operations {
	o := Operations{
		Backups:  OperationsBackups{RetentionDays: DefaultRetentionDays, CopyToRegion: c.CopyToRegion},
		Recovery: OperationsRecovery{RPO: DefaultRPO, RTO: DefaultRTO},
		Alerts:   OperationsAlerts{Email: c.Email},
	}
	if c.RetentionDays != nil {
		o.Backups.RetentionDays = *c.RetentionDays
	}
	if c.RPO != "" {
		o.Recovery.RPO = c.RPO
	}
	if c.RTO != "" {
		o.Recovery.RTO = c.RTO
	}
	return o
}

// The patterns of the CRD's markers on OperationsSpec; a test holds the two
// together.
var (
	objectivePattern = regexp.MustCompile(`^[0-9]+(m|h|d)$`)
	regionPattern    = regexp.MustCompile(`^([a-z]{2}(-[a-z]+)+-[0-9]+)?$`)
	emailPattern     = regexp.MustCompile(`^([^@\s]+@[^@\s]+)?$`)
)

// OperationsProblems returns what the API server would refuse in c, one
// sentence per field, naming it as spec.operations does: the ranges, the
// patterns and the lengths of the CRD's markers (#1210).
func OperationsProblems(c OperationsChoices) []string {
	var out []string
	if d := c.RetentionDays; d != nil && (*d < 0 || *d > MaxRetentionDays) {
		out = append(out, fmt.Sprintf("backups.retentionDays is %d; write 0 to %d days, 0 meaning point-in-time "+
			"recovery only", *d, MaxRetentionDays))
	}
	tooLong := func(name, value string, most int) string {
		return fmt.Sprintf("%s is %d characters; the most is %d", name, utf8.RuneCountInString(value), most)
	}
	switch {
	case !regionPattern.MatchString(c.CopyToRegion):
		out = append(out, fmt.Sprintf("backups.copyToRegion is %q; write an AWS region such as eu-west-1, "+
			"or leave it out", c.CopyToRegion))
	case utf8.RuneCountInString(c.CopyToRegion) > MaxCopyToRegionLength:
		out = append(out, tooLong("backups.copyToRegion", c.CopyToRegion, MaxCopyToRegionLength))
	}
	for _, f := range []struct{ name, value string }{{"recovery.rpo", c.RPO}, {"recovery.rto", c.RTO}} {
		switch {
		case f.value == "":
		case !objectivePattern.MatchString(f.value):
			out = append(out, fmt.Sprintf("%s is %q; write a number of minutes, hours or days, such as 30m, "+
				"4h or 1d", f.name, f.value))
		case utf8.RuneCountInString(f.value) > MaxObjectiveLength:
			out = append(out, tooLong(f.name, f.value, MaxObjectiveLength))
		}
	}
	switch {
	case !emailPattern.MatchString(c.Email):
		out = append(out, fmt.Sprintf("alerts.email is %q; write one email address, or leave it out", c.Email))
	case utf8.RuneCountInString(c.Email) > MaxEmailLength:
		out = append(out, tooLong("alerts.email", c.Email, MaxEmailLength))
	}
	return out
}
