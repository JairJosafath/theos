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

// Package buildinfo holds what a build stamps into the binary. The operator's
// Dockerfile sets Version from its VERSION build argument:
//
//	go build -ldflags=-X=github.com/JairJosafath/theos/internal/buildinfo.Version=1.2.3
//
// A build that sets nothing, such as go run or a test, is DevVersion.
package buildinfo

// DevVersion is the version of a build no release stamped.
const DevVersion = "dev"

// Version is the release this binary was built from, DevVersion unless the
// build set it with -ldflags -X.
var Version = DevVersion
