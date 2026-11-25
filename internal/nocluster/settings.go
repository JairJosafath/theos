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

package nocluster

import (
	"fmt"
	"slices"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/JairJosafath/theos/features"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
)

// Settings are model/settings.yaml: what no CRD holds (decision 12), each
// with a default that needs no cluster. The decode is strict, so a key a
// later step reads (merge) is added by that step.
type Settings struct {
	// Repository is the project repository's name; theos-<project> unless set.
	Repository string `json:"repository,omitempty"`
	// Lease is how long a session's claim holds the work, and Grace how long
	// past it another session waits (docs/plans/lead-runtime.md, decision
	// 8): Go durations, 60m and 5m unless set.
	Lease string `json:"lease,omitempty"`
	Grace string `json:"grace,omitempty"`
	// Claimants are the accounts whose markers count; the credential's own
	// login unless set (decision 10).
	Claimants []string `json:"claimants,omitempty"`
	// Operator holds the four flags an operator renders every scaffold with
	// (cmd/main.go), so a Container scaffolded here is what an operator with
	// the same flags scaffolds.
	Operator OperatorSettings `json:"operator,omitzero"`
}

// OperatorSettings are the operator's --session-feature,
// --session-binary-url, --ci-image-go and --ci-image-base, with their
// defaults.
type OperatorSettings struct {
	// SessionFeature is how a devcontainer gets the Session client: local
	// (the default), none, or a feature reference.
	SessionFeature string `json:"sessionFeature,omitempty"`
	// SessionBinaryURL is where the feature downloads theos-session; "" for none.
	SessionBinaryURL string `json:"sessionBinaryURL,omitempty"`
	// CIImageGo and CIImageBase are the images a GitHub check falls back to;
	// "" for the release's own (model.CIImages.OrDefault).
	CIImageGo   string `json:"ciImageGo,omitempty"`
	CIImageBase string `json:"ciImageBase,omitempty"`
}

// DecodeSettings reads model/settings.yaml strictly, data empty for none,
// and fills in each default for project.
func DecodeSettings(data []byte, project string) (Settings, error) {
	var s Settings
	if err := yaml.UnmarshalStrict(data, &s); err != nil {
		return Settings{}, fmt.Errorf("%s: %w", SettingsFile, err)
	}
	if s.Repository == "" {
		s.Repository = "theos-" + project
	}
	if s.Operator.SessionFeature == "" {
		s.Operator.SessionFeature = features.SessionFeatureLocal
	}
	if s.Lease == "" {
		s.Lease = model.ClaimLease.String()
	}
	if s.Grace == "" {
		s.Grace = model.ClaimGrace.String()
	}
	if _, err := s.ClaimRules(""); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// ClaimRules are the rules the markers count by: the lease, the grace, and
// the claimants, login's alone unless the settings name them.
func (s Settings) ClaimRules(login string) (model.ClaimRules, error) {
	rules := model.DefaultClaimRules(login)
	var err error
	if rules.Lease, err = positive(s.Lease, "lease"); err != nil {
		return rules, err
	}
	if rules.Grace, err = positive(s.Grace, "grace"); err != nil {
		return rules, err
	}
	if len(s.Claimants) > 0 {
		rules.Claimants = slices.Clone(s.Claimants)
	}
	return rules, nil
}

// positive reads a duration setting, which must be above zero.
func positive(value, key string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s: %s %q is not a positive duration such as 60m", SettingsFile, key, value)
	}
	return d, nil
}

// Scaffold is what every Container is rendered with, as an operator with
// these flags renders it.
func (s Settings) Scaffold() (modelfacts.Settings, error) {
	opts, err := features.ScaffoldOptions(s.Operator.SessionFeature, s.Operator.SessionBinaryURL)
	if err != nil {
		return modelfacts.Settings{}, fmt.Errorf("%s: operator.sessionFeature: %w", SettingsFile, err)
	}
	return modelfacts.Settings{Options: opts,
		CIImages: model.CIImages{Go: s.Operator.CIImageGo, Base: s.Operator.CIImageBase}}, nil
}
