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

	"sigs.k8s.io/yaml"

	"github.com/JairJosafath/theos/features"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
)

// Settings are model/settings.yaml: what no CRD holds (decision 12), each
// with a default that needs no cluster. The decode is strict, so a key a
// later step reads (lease, grace, claimants, merge) is added by that step.
type Settings struct {
	// Repository is the project repository's name; theos-<project> unless set.
	Repository string `json:"repository,omitempty"`
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
	return s, nil
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
