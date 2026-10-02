package model

import (
	"fmt"
	"strings"
)

// C4Element is one element of the story.
type C4Element struct {
	// Kind is Person, SoftwareSystem, Container or DataStore.
	Kind        string
	Name        string
	DisplayName string
	Description string
	// Type is a Container's type.
	Type string
}

func (e C4Element) title() string {
	if e.DisplayName != "" {
		return e.DisplayName
	}
	return e.Name
}

// C4Link is a Relationship of the Container an agent works on.
type C4Link struct {
	// Outgoing means the Container is the Relationship's source.
	Outgoing    bool
	Other       C4Element
	Description string
	// RepositoryURL is the other end's repository, for a Container.
	RepositoryURL string
}

// C4Context is what an agent is told about the place of its work in the story.
type C4Context struct {
	System    C4Element
	Needs     []string
	KPIs      []string
	Users     []C4Element
	Container C4Element
	Links     []C4Link
}

// Text is the context as agents read it.
func (c C4Context) Text() string {
	var b strings.Builder
	if c.System.Name != "" {
		fmt.Fprintf(&b, "System: %s", c.System.title())
		if c.System.Description != "" {
			fmt.Fprintf(&b, " — %s", c.System.Description)
		}
		b.WriteString("\n")
	}
	for _, need := range c.Needs {
		fmt.Fprintf(&b, "Need: %s\n", need)
	}
	for _, kpi := range c.KPIs {
		fmt.Fprintf(&b, "KPI: %s\n", kpi)
	}
	for _, user := range c.Users {
		fmt.Fprintf(&b, "Used by: %s", user.title())
		if user.Description != "" {
			fmt.Fprintf(&b, " — %s", user.Description)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Application: %s (%s)", c.Container.title(), c.Container.Type)
	if c.Container.Description != "" {
		fmt.Fprintf(&b, " — %s", c.Container.Description)
	}
	if len(c.Links) > 0 {
		b.WriteString("\nRelationships:")
	}
	for _, link := range c.Links {
		other := fmt.Sprintf("%s (%s %s", link.Other.title(), link.Other.Kind, link.Other.Name)
		if link.Other.Type != "" {
			other += ", " + link.Other.Type
		}
		other += ")"
		if link.Outgoing {
			fmt.Fprintf(&b, "\n- This application uses %s", other)
		} else {
			fmt.Fprintf(&b, "\n- %s uses this application", other)
		}
		if link.Description != "" {
			fmt.Fprintf(&b, ": %s", strings.TrimSuffix(link.Description, "."))
		}
		b.WriteString(".")
		if link.RepositoryURL != "" {
			fmt.Fprintf(&b, " Repository: %s", link.RepositoryURL)
		}
	}
	return b.String()
}

// RelatedRepositories are the repositories of the Containers the Container has
// Relationships with, by Container name: agents may read them.
func (c C4Context) RelatedRepositories() map[string]string {
	repos := map[string]string{}
	for _, link := range c.Links {
		if link.Other.Kind == "Container" && link.RepositoryURL != "" {
			repos[link.Other.Name] = link.RepositoryURL
		}
	}
	return repos
}
