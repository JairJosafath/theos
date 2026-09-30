package scaffold

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Identity is the content of .c4factory.yaml: which Container a repository is.
// The Session client reads it to register Sessions on the right Container.
type Identity struct {
	Project        string   `yaml:"project"`
	SoftwareSystem string   `yaml:"softwareSystem"`
	Container      string   `yaml:"container"`
	Type           string   `yaml:"type"`
	ScaffoldedFrom []string `yaml:"scaffoldedFrom"`
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
)

// Author of the initial commit.
const (
	AuthorName  = "C4 Factory"
	AuthorEmail = "factory@c4.factory.io"
)

// CommitMessage is the message of the single initial commit. Its trailers let the
// operator recognise its own commit when it retries, and recover what it recorded.
func CommitMessage(in Input, scaffoldedFrom, relationships []string) string {
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
	return b.String()
}

// CommitInfo is what the operator reads back from its own commit.
type CommitInfo struct {
	// Container is project/container.
	Container      string
	ScaffoldedFrom []string
	// Relationships are the Relationships whose Connectors were applied.
	Relationships []string
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
			info.Relationships = append(info.Relationships, strings.TrimSpace(value))
		}
	}
	if info.Container == "" {
		return CommitInfo{}, ErrNotOurs
	}
	return info, nil
}
