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

// Package bases embeds the CRDs make manifests writes beside this file, so
// the theos binary defaults a model's objects from the same schemas the API
// server does (docs/plans/no-cluster.md, decision 2). This file is not
// generated: make manifests rewrites only the YAML here.
package bases

import "embed"

// FS holds every CRD of this directory, one theos.factory.io_<plural>.yaml a kind.
//
//go:embed *.yaml
var FS embed.FS
