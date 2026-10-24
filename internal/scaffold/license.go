package scaffold

import (
	"embed"
	"fmt"
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
		if strings.HasPrefix(strings.ToLower(line), "copyright") {
			lines[i] = fmt.Sprintf("Copyright (c) %d %s\n", l.Year, l.Holder)
			return []byte(strings.Join(lines, "")), nil
		}
	}
	return nil, fmt.Errorf("license %s has no copyright line to fill", l.ID)
}
