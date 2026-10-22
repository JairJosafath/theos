package scaffold

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/c4factory/internal/model"
)

// Identity is the content of .c4factory.yaml: which Container a repository is.
// The Session client reads it to register Sessions on the right Container.
type Identity struct {
	Project        string   `yaml:"project"`
	SoftwareSystem string   `yaml:"softwareSystem"`
	Container      string   `yaml:"container"`
	Type           string   `yaml:"type"`
	ScaffoldedFrom []string `yaml:"scaffoldedFrom"`
	// Checks is when CI ran the full check as the Standard's files were
	// scaffolded: EveryChange or BeforeRelease. Only a Container with a Standard
	// has it.
	Checks string `yaml:"checks,omitempty"`
}

const identityHeader = "# Written once by C4 Factory when this repository was scaffolded.\n" +
	"# The Session client reads it to find this repository's Container.\n"

// Marshal returns the identity file's content.
func (id Identity) Marshal() ([]byte, error) {
	data, err := yaml.Marshal(id)
	if err != nil {
		return nil, err
	}
	return append([]byte(identityHeader), data...), nil
}

// ParseIdentity reads an identity file.
func ParseIdentity(data []byte) (Identity, error) {
	var id Identity
	if err := yaml.Unmarshal(data, &id); err != nil {
		return Identity{}, fmt.Errorf("parse %s: %w", IdentityPath, err)
	}
	if id.Project == "" || id.Container == "" {
		return Identity{}, fmt.Errorf("parse %s: project and container are required", IdentityPath)
	}
	return id, nil
}

// Commit trailers that mark the operator's own initial commit.
const (
	trailerContainer    = "C4-Factory-Container"
	trailerFrom         = "Scaffolded-From"
	trailerRelationship = "Applied-Relationship"
	trailerBundle       = "Scaffolded-Bundle"
)

// Author of the initial commit.
const (
	AuthorName  = "C4 Factory"
	AuthorEmail = "factory@c4.factory.io"
)

// CommitMessage is the message of the single initial commit. Its trailers let the
// operator recognise its own commit when it retries, and recover what it recorded.
func CommitMessage(in Input, scaffoldedFrom []string, relationships []model.AppliedRelationship) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Scaffold %s\n\n", in.Container)
	fmt.Fprintf(&b, "Created by C4 Factory for Container %s in SoftwareSystem %s.\n", in.Container, in.SoftwareSystem)
	b.WriteString("Scaffolding happens once; from here on the repository is yours.\n\n")
	fmt.Fprintf(&b, "%s: %s/%s\n", trailerContainer, in.Project, in.Container)
	for _, from := range scaffoldedFrom {
		fmt.Fprintf(&b, "%s: %s\n", trailerFrom, from)
	}
	for _, rel := range relationships {
		fmt.Fprintf(&b, "%s: %s\n", trailerRelationship, rel)
	}
	if checks := scaffoldedChecks(in); checks != "" {
		fmt.Fprintf(&b, "%s: %s\n", trailerChecks, checks)
	}
	if in.Bundle != "" {
		fmt.Fprintf(&b, "%s: %s\n", trailerBundle, scaffoldedBundle(in))
	}
	return b.String()
}

// CommitInfo is what the operator reads back from its own commit.
type CommitInfo struct {
	// Container is project/container.
	Container      string
	ScaffoldedFrom []string
	// Relationships are the Relationships whose Connectors were applied. Commits
	// made before targets were recorded give only their names.
	Relationships []model.AppliedRelationship
	// Checks is when CI runs the full check as the Standard's files were
	// scaffolded; commits made before it was recorded, or of a Container without
	// a Standard, leave it empty.
	Checks string
	// Bundle is the Bundle the Container's entries were expanded from, as they
	// were at the scaffold; nil for a Container naming its sides, or a commit
	// made before it was recorded.
	Bundle *model.Bundle
}

// ErrNotOurs means a commit wasn't made by the operator.
var ErrNotOurs = errors.New("commit was not made by C4 Factory")

// ParseCommitMessage reads the trailers of an initial commit, or returns ErrNotOurs.
func ParseCommitMessage(msg string) (CommitInfo, error) {
	var info CommitInfo
	scanner := bufio.NewScanner(strings.NewReader(msg))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ": ")
		if !ok {
			continue
		}
		switch key {
		case trailerContainer:
			info.Container = strings.TrimSpace(value)
		case trailerFrom:
			info.ScaffoldedFrom = append(info.ScaffoldedFrom, strings.TrimSpace(value))
		case trailerRelationship:
			info.Relationships = append(info.Relationships, model.ParseAppliedRelationship(value))
		case trailerChecks:
			info.Checks = strings.TrimSpace(value)
		case trailerBundle:
			bundle := model.ParseBundle(value)
			info.Bundle = &bundle
		}
	}
	if info.Container == "" {
		return CommitInfo{}, ErrNotOurs
	}
	return info, nil
}

// scaffoldedChecks is when CI runs the full check as in's Standard files
// render, or "" for a Container without a Standard.
func scaffoldedChecks(in Input) string {
	if in.Standard == "" {
		return ""
	}
	return model.StandardVersion{Checks: in.Checks}.Normal().Checks
}

// scaffoldedBundle is the Bundle in's entries were expanded from, as the
// commit records it.
func scaffoldedBundle(in Input) model.Bundle {
	return model.Bundle{Name: in.Bundle, Framework: in.Framework, Standard: in.Standard,
		Compute: in.Compute, Deployment: in.Deployment}
}
