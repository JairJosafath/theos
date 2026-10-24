package scaffold

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/c4factory/internal/model"
)

// domainLayers are two Domains after the webshop layers, sorted by name.
func domainLayers() []Layer {
	return append(webshopLayers(),
		Layer{Side: model.SideDomain, Name: "billing", Ref: "v0.2.0", Files: tree(
			"README.md", "# Billing\n",
		)},
		Layer{Side: model.SideDomain, Name: "webshop", Ref: "v0.1.0", Files: tree(
			"README.md.tmpl", "# Webshop\n\nFor {{ .Container }}.\n",
			"carts.md", "# Carts\n",
			".c4factory/README.md", "about the template",
		)},
	)
}

func withDomains(in Input, domains ...string) Input {
	in.Domains = domains
	return in
}

func TestBuildDomainsLandUnderTheirDirectory(t *testing.T) {
	res, err := Build(withDomains(ordersAPI, "billing", "webshop"), domainLayers(), Options{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	for _, p := range []string{
		"docs/agents/domain/billing/README.md",
		"docs/agents/domain/webshop/README.md",
		"docs/agents/domain/webshop/carts.md",
	} {
		if _, ok := res.Files[p]; !ok {
			t.Errorf("Build() lacks %s; paths = %v", p, res.Files.Paths())
		}
	}
	if got := string(res.Files["docs/agents/domain/webshop/README.md"].Data); got != "# Webshop\n\nFor orders-api.\n" {
		t.Errorf("README = %q", got)
	}
	for _, p := range res.Files.Paths() {
		if strings.HasPrefix(p, "docs/agents/domain/") {
			continue
		}
		if strings.HasSuffix(p, "carts.md") || strings.Contains(p, "about the template") {
			t.Errorf("Domain file %s rendered outside its directory", p)
		}
	}
	wantTail := []string{"billing@v0.2.0", "webshop@v0.1.0"}
	if n := len(res.ScaffoldedFrom); !slices.Equal(res.ScaffoldedFrom[n-2:], wantTail) {
		t.Errorf("ScaffoldedFrom = %v, want it to end with %v", res.ScaffoldedFrom, wantTail)
	}
}

func TestBuildDomainFailures(t *testing.T) {
	tests := []struct {
		name   string
		in     Input
		mutate func([]Layer) []Layer
		check  func(t *testing.T, err error)
	}{
		{
			name: "a non-Markdown file (#1757)",
			in:   withDomains(ordersAPI, "billing", "webshop"),
			mutate: func(l []Layer) []Layer {
				l[5].Files["notes.txt"] = File{Data: []byte("x")}
				return l
			},
			check: func(t *testing.T, err error) {
				e, ok := errors.AsType[*DomainNotMarkdownError](err)
				if !ok {
					t.Fatalf("Build() error = %v, want *DomainNotMarkdownError", err)
				}
				if e.Domain != "webshop" || e.Path != "notes.txt" {
					t.Errorf("error = %+v, want Domain webshop, Path notes.txt", e)
				}
				if want := "Domain webshop renders notes.txt, which is not Markdown: a Domain holds only *.md files"; err.Error() != want {
					t.Errorf("message = %q, want %q", err.Error(), want)
				}
			},
		},
		{
			name: "a template that renders to no Markdown name",
			in:   withDomains(ordersAPI, "billing", "webshop"),
			mutate: func(l []Layer) []Layer {
				l[5].Files["index.html.tmpl"] = File{Data: []byte("{{ .Container }}")}
				return l
			},
			check: func(t *testing.T, err error) {
				if e, ok := errors.AsType[*DomainNotMarkdownError](err); !ok || e.Path != "index.html" {
					t.Fatalf("Build() error = %v, want *DomainNotMarkdownError for index.html", err)
				}
			},
		},
		{
			name: "no README.md (#1757)",
			in:   withDomains(ordersAPI, "billing", "webshop"),
			mutate: func(l []Layer) []Layer {
				delete(l[4].Files, "README.md")
				l[4].Files["overview.md"] = File{Data: []byte("# Billing\n")}
				return l
			},
			check: func(t *testing.T, err error) {
				e, ok := errors.AsType[*DomainNoReadmeError](err)
				if !ok {
					t.Fatalf("Build() error = %v, want *DomainNoReadmeError", err)
				}
				if e.Domain != "billing" || err.Error() != "Domain billing renders no README.md" {
					t.Errorf("error = %q (%+v)", err.Error(), e)
				}
			},
		},
		{
			name: "a README left out by its template is no README",
			in:   withDomains(ordersAPI, "billing", "webshop"),
			mutate: func(l []Layer) []Layer {
				l[5].Files["README.md.tmpl"] = File{Data: []byte("{{ if eq .Type \"job\" }}# Webshop{{ end }}")}
				return l
			},
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*DomainNoReadmeError](err); !ok {
					t.Fatalf("Build() error = %v, want *DomainNoReadmeError", err)
				}
			},
		},
		{
			name: "another layer writes into a Domain's directory",
			in:   withDomains(ordersAPI, "billing", "webshop"),
			mutate: func(l []Layer) []Layer {
				l[0].Files["docs/agents/domain/webshop/README.md"] = File{Data: []byte("x")}
				return l
			},
			check: func(t *testing.T, err error) {
				if _, ok := errors.AsType[*ConflictError](err); !ok {
					t.Fatalf("Build() error = %v, want *ConflictError", err)
				}
				if want := "python-fastapi and webshop both write docs/agents/domain/webshop/README.md"; err.Error() != want {
					t.Errorf("message = %q, want %q", err.Error(), want)
				}
			},
		},
		{
			name: "a Domain before a Connector is out of order",
			in:   withDomains(ordersAPI, "billing", "webshop"),
			mutate: func(l []Layer) []Layer {
				l[3], l[4] = l[4], l[3]
				return l
			},
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "out of order") {
					t.Fatalf("Build() error = %v, want out of order", err)
				}
			},
		},
		{
			name:   "domain layers must match the input, sorted",
			in:     withDomains(ordersAPI, "webshop", "billing"),
			mutate: func(l []Layer) []Layer { return l },
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "domain layers [billing webshop] don't match input domains [webshop billing]") {
					t.Fatalf("Build() error = %v, want a domain mismatch", err)
				}
			},
		},
		{
			name:   "a Domain layer the input doesn't name",
			in:     ordersAPI,
			mutate: func(l []Layer) []Layer { return l },
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "don't match input domains") {
					t.Fatalf("Build() error = %v, want a domain mismatch", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(tt.in, tt.mutate(domainLayers()), Options{})
			tt.check(t, err)
		})
	}
}
