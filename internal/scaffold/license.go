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

package scaffold

import (
	"embed"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// LicensePath is the license file the operator writes at the repository's
// root when the Container's spec.license, else its Project's, names one. It
// is the app's file: a published source carries it, and agents may change it.
const LicensePath = "LICENSE"

// License is the license a repository is scaffolded with. The controller
// fills it (ResolveLicense); an empty ID means none is set and Build writes
// no LICENSE. Templates read it as {{ .License.ID }}.
type License struct {
	// ID is the SPDX id, one of LicenseIDs.
	ID string
	// Holder is the copyright holder: the Project's displayName, else its name.
	Holder string
	// Year is the scaffold's year.
	Year int
}

// ResolveLicense is the license a Container is scaffolded with: its own
// spec.license, else its Project's, the holder the Project's displayName,
// else its name, and the year of now. With neither id set it is empty.
func ResolveLicense(containerID, projectID, projectDisplayName, projectName string, now time.Time) License {
	id := containerID
	if id == "" {
		id = projectID
	}
	if id == "" {
		return License{}
	}
	holder := projectDisplayName
	if holder == "" {
		holder = projectName
	}
	return License{ID: id, Holder: holder, Year: now.Year()}
}

// licenseTexts holds the SPDX license list's plain text of every license the
// schema's enum offers, verbatim (https://github.com/spdx/license-list-data,
// text/). Adding a license is a file here and an entry in the enum.
//
//go:embed licenses/*.txt
var licenseTexts embed.FS

// LicenseIDs returns the SPDX ids the operator holds the text of, sorted.
func LicenseIDs() []string {
	entries, err := licenseTexts.ReadDir("licenses")
	if err != nil {
		panic(err) // the embedded directory always reads
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, strings.TrimSuffix(e.Name(), ".txt"))
	}
	slices.Sort(ids)
	return ids
}

// copyrightLicenses are the ids whose text opens with a copyright line the
// scaffold fills with the year and the holder. The other texts are written
// as published, Apache-2.0's appendix placeholders included.
var copyrightLicenses = []string{"MIT", "ISC", "BSD-2-Clause", "BSD-3-Clause", "0BSD"}

// copyrightPlaceholders matches the published copyright line of the
// copyrightLicenses up to its placeholders for the year and the holder
// (<year> <owner>, <year> <copyright holders>, YEAR by AUTHOR EMAIL); what
// follows them on the line (BSD-3-Clause's period) is kept.
var copyrightPlaceholders = regexp.MustCompile(
	`(?i)^copyright\s+(?:\(c\)\s+)?(?:<year>|YEAR)\s+(?:by\s+)?(?:<owner>|<copyright holders>|AUTHOR EMAIL)`)

// licenseText is the content of LICENSE for l.
func licenseText(l License) ([]byte, error) {
	data, err := licenseTexts.ReadFile("licenses/" + l.ID + ".txt")
	if err != nil {
		return nil, fmt.Errorf("no text for license %q", l.ID)
	}
	if !slices.Contains(copyrightLicenses, l.ID) {
		return data, nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	for i, line := range lines {
		if m := copyrightPlaceholders.FindStringIndex(line); m != nil {
			rest := strings.TrimRight(line[m[1]:], " \n")
			lines[i] = fmt.Sprintf("Copyright (c) %d %s%s\n", l.Year, l.Holder, rest)
			return []byte(strings.Join(lines, "")), nil
		}
	}
	return nil, fmt.Errorf("license %s has no copyright line to fill", l.ID)
}
