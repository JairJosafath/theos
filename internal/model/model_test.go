package model

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func entry(side, name string, types ...string) CatalogEntry {
	return CatalogEntry{Side: side, Name: name, SupportedTypes: types, TemplateRef: "v1.0.0"}
}

func TestCheckChoices(t *testing.T) {
	fastapi := entry(SideFramework, "python-fastapi", TypeService)
	small := entry(SideCompute, "small", TypeService, TypeWorker, TypeJob)
	manifests := entry(SideDeployment, "k8s-manifests", TypeService, TypeWorker)
	postgres := entry(SideConnector, "postgres", TypeService, TypeWorker, TypeJob)

	tests := []struct {
		name    string
		typ     string
		entries []CatalogEntry
		want    string
	}{
		{"all support service", TypeService, []CatalogEntry{fastapi, small, manifests, postgres}, ""},
		{"framework rejects static-site", TypeStaticSite, []CatalogEntry{fastapi},
			"python-fastapi does not support static-site"},
		{"several reject", TypeStaticSite, []CatalogEntry{fastapi, small, manifests},
			"python-fastapi, small and k8s-manifests do not support static-site"},
		{"connector reached through a relationship rejects", TypeJob, []CatalogEntry{small, postgres, manifests},
			"k8s-manifests does not support job"},
		{"no entries", TypeWorker, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckChoices(tt.typ, tt.entries)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("CheckChoices() = %v, want nil", err)
				}
				return
			}
			if _, ok := errors.AsType[*ChoiceError](err); !ok {
				t.Fatalf("CheckChoices() = %v, want *ChoiceError", err)
			}
			if err.Error() != tt.want {
				t.Errorf("message = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestDataStoresUsedBy(t *testing.T) {
	rel := func(name, sk, sn, tk, tn string) Relationship {
		return Relationship{Name: name, Source: Ref{sk, sn}, Target: Ref{tk, tn}}
	}
	connectorOf := map[string]string{"orders-db": "postgres", "audit-db": "postgres", "cache": "redis"}

	tests := []struct {
		name string
		rels []Relationship
		want DataStoreUse
	}{
		{
			name: "one datastore",
			rels: []Relationship{rel("orders-api-to-orders-db", KindContainer, "orders-api", KindDataStore, "orders-db")},
			want: DataStoreUse{Connectors: []string{"postgres"}, Relationships: []AppliedRelationship{{"orders-api-to-orders-db", "orders-db", "postgres"}}},
		},
		{
			name: "same connector through two datastores applies once",
			rels: []Relationship{
				rel("b", KindContainer, "orders-api", KindDataStore, "audit-db"),
				rel("a", KindContainer, "orders-api", KindDataStore, "orders-db"),
				rel("c", KindContainer, "orders-api", KindDataStore, "cache"),
			},
			want: DataStoreUse{Connectors: []string{"postgres", "redis"}, Relationships: []AppliedRelationship{
				{"a", "orders-db", "postgres"}, {"b", "audit-db", "postgres"}, {"c", "cache", "redis"},
			}},
		},
		{
			name: "story-only links and other containers are ignored",
			rels: []Relationship{
				rel("p", KindPerson, "customer", KindSoftwareSystem, "webshop-core"),
				rel("cc", KindContainer, "orders-api", KindContainer, "web"),
				rel("other", KindContainer, "web", KindDataStore, "orders-db"),
			},
			want: DataStoreUse{},
		},
		{
			name: "missing datastore is reported",
			rels: []Relationship{rel("x", KindContainer, "orders-api", KindDataStore, "ghost")},
			want: DataStoreUse{MissingDataStores: []string{"ghost"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DataStoresUsedBy("orders-api", tt.rels, connectorOf)
			if !slices.Equal(got.Connectors, tt.want.Connectors) ||
				!slices.Equal(got.Relationships, tt.want.Relationships) ||
				!slices.Equal(got.MissingDataStores, tt.want.MissingDataStores) {
				t.Errorf("DataStoresUsedBy() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSummary(t *testing.T) {
	tests := []struct {
		ready, total, sessions int
		want                   string
	}{
		{3, 4, 2, "3 of 4 containers Ready, 2 active sessions"},
		{1, 1, 1, "1 of 1 container Ready, 1 active session"},
		{0, 0, 0, "0 of 0 containers Ready, 0 active sessions"},
	}
	for _, tt := range tests {
		if got := Summary(tt.ready, tt.total, tt.sessions); got != tt.want {
			t.Errorf("Summary(%d, %d, %d) = %q, want %q", tt.ready, tt.total, tt.sessions, got, tt.want)
		}
	}
}

func TestSessionFreshness(t *testing.T) {
	created := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		heartbeat     time.Time
		now           time.Time
		wantStale     bool
		wantRemaining time.Duration
	}{
		{"fresh heartbeat", created.Add(time.Minute), created.Add(90 * time.Second), false, 90 * time.Second},
		{"exactly at the window", created, created.Add(DefaultStaleAfter), true, 0},
		{"long gone", created, created.Add(time.Hour), true, 0},
		{"no heartbeat yet counts from creation", time.Time{}, created.Add(30 * time.Second), false, 90 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stale, remaining := SessionFreshness(tt.heartbeat, created, tt.now, DefaultStaleAfter)
			if stale != tt.wantStale || remaining != tt.wantRemaining {
				t.Errorf("SessionFreshness() = (%v, %v), want (%v, %v)", stale, remaining, tt.wantStale, tt.wantRemaining)
			}
		})
	}
}

func TestAppliedRelationship(t *testing.T) {
	applied := AppliedRelationship{Name: "orders-api-to-orders-db", DataStore: "orders-db", Connector: "postgres"}
	nameOnly := AppliedRelationship{Name: "orders-api-to-orders-db"}

	for _, a := range []AppliedRelationship{applied, nameOnly} {
		if got := ParseAppliedRelationship(a.String()); got != a {
			t.Errorf("ParseAppliedRelationship(%q) = %+v, want %+v", a.String(), got, a)
		}
	}
	if got := applied.String(); got != "orders-api-to-orders-db DataStore/orders-db Connector/postgres" {
		t.Errorf("String() = %q", got)
	}

	tests := []struct {
		name                 string
		record               AppliedRelationship
		dataStore, connector string
		want                 bool
	}{
		{"same target", applied, "orders-db", "postgres", true},
		{"recreated with another DataStore", applied, "cache-db", "redis", false},
		{"DataStore recreated with another Connector", applied, "orders-db", "redis", false},
		{"DataStore gone", applied, "orders-db", "", true},
		{"name-only record", nameOnly, "cache-db", "redis", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.record.StillApplied(tt.dataStore, tt.connector); got != tt.want {
				t.Errorf("StillApplied(%q, %q) = %v, want %v", tt.dataStore, tt.connector, got, tt.want)
			}
		})
	}
}

func TestCheckStandard(t *testing.T) {
	standard := CatalogEntry{Side: SideStandard, Name: "strict-standard", SupportedFrameworks: []string{"go-cli"}}
	tests := []struct {
		name      string
		framework string
		standard  *CatalogEntry
		want      string
	}{
		{"no standard", "python-fastapi", nil, ""},
		{"listed framework", "go-cli", &standard, ""},
		{"unlisted framework", "python-fastapi", &standard, "strict-standard does not support python-fastapi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckStandard(tt.framework, tt.standard)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("CheckStandard() = %v, want nil", err)
				}
				return
			}
			if _, ok := errors.AsType[*FitError](err); !ok {
				t.Fatalf("CheckStandard() = %v, want *FitError", err)
			}
			if err.Error() != tt.want {
				t.Errorf("message = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestClusterNamespace(t *testing.T) {
	tests := []struct {
		host string
		want string
		ok   bool
	}{
		{"gitea.gitea.svc.cluster.local", "gitea", true},
		{"gitea.git-host.svc", "git-host", true},
		{"gitea.example.com", "", false},
		{"localhost", "", false},
	}
	for _, tt := range tests {
		got, ok := ClusterNamespace(tt.host)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ClusterNamespace(%q) = %q, %v; want %q, %v", tt.host, got, ok, tt.want, tt.ok)
		}
	}
}

func TestAgentProtectedPaths(t *testing.T) {
	got := AgentProtectedPaths([]string{"Makefile", ".gitea/", "docs/style/"})
	want := []string{".c4factory.yaml", ".devcontainer/", ".gitea/", "Makefile", "docs/style/"}
	if !slices.Equal(got, want) {
		t.Errorf("AgentProtectedPaths() = %v, want %v", got, want)
	}
	if !slices.Equal(AgentProtectedPaths(nil), PlatformProtectedPaths) {
		t.Errorf("AgentProtectedPaths(nil) = %v, want the platform's", AgentProtectedPaths(nil))
	}
}

func TestTouchedPaths(t *testing.T) {
	protected := []string{".gitea/", "Makefile"}
	tests := []struct {
		changed []string
		want    []string
	}{
		{[]string{"lib/main.dart", "README.md"}, nil},
		{[]string{".gitea/workflows/check.yaml", "lib/a.dart"}, []string{".gitea/workflows/check.yaml"}},
		{[]string{"Makefile"}, []string{"Makefile"}},
		{[]string{"Makefile.local", "tools/Makefile", ".gitea"}, nil},
	}
	for _, tt := range tests {
		if got := TouchedPaths(tt.changed, protected); !slices.Equal(got, tt.want) {
			t.Errorf("TouchedPaths(%v) = %v, want %v", tt.changed, got, tt.want)
		}
	}
}

func TestUnpassedChecks(t *testing.T) {
	const pr, push = "check / check (pull_request)", "check / check (push)"
	tests := []struct {
		name     string
		contexts map[string]string
		want     []string
	}{
		{name: "a passing check", contexts: map[string]string{pr: "success"}},
		{name: "a skipped push check next to a passing one", contexts: map[string]string{pr: "success", push: "skipped"}},
		{name: "only skipped checks", contexts: map[string]string{pr: "skipped", push: "skipped"}, want: []string{"check*"}},
		{name: "no checks", want: []string{"check*"}},
		{name: "no check matches", contexts: map[string]string{"CI / check (pull_request)": "success"}, want: []string{"check*"}},
		{name: "a pending match", contexts: map[string]string{pr: "success", push: "pending"}, want: []string{"check*"}},
		{name: "a failed match", contexts: map[string]string{pr: "success", push: "failure"}, want: []string{"check*"}},
		{name: "an errored match", contexts: map[string]string{pr: "error", push: "skipped"}, want: []string{"check*"}},
		{name: "a warning match", contexts: map[string]string{pr: "success", push: "warning"}, want: []string{"check*"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnpassedChecks(tt.contexts); !slices.Equal(got, tt.want) {
				t.Errorf("UnpassedChecks(%v) = %v, want %v", tt.contexts, got, tt.want)
			}
		})
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"check*", "check / check (pull_request)", true},
		{"check*", "check", true},
		{"check*", "CI / check (pull_request)", false},
		{"*check*", "CI / check (pull_request)", true},
		{"check / *(push)", "check / check (pull_request)", false},
		{"a*a", "a", false},
		{"check", "check", true},
		{"check", "checks", false},
	}
	for _, tt := range tests {
		if got := matchGlob(tt.pattern, tt.s); got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}
