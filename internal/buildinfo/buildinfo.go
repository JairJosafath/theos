/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

// Package buildinfo holds what a build stamps into the binary. The operator's
// Dockerfile sets Version from its VERSION build argument:
//
//	go build -ldflags=-X=github.com/JairJosafath/c4factory/internal/buildinfo.Version=1.2.3
//
// A build that sets nothing, such as go run or a test, is DevVersion.
package buildinfo

// DevVersion is the version of a build no release stamped.
const DevVersion = "dev"

// Version is the release this binary was built from, DevVersion unless the
// build set it with -ldflags -X.
var Version = DevVersion
