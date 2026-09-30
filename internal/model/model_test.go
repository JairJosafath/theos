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
			var choiceErr *ChoiceError
			if !errors.As(err, &choiceErr) {
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
			want: DataStoreUse{Connectors: []string{"postgres"}, Relationships: []string{"orders-api-to-orders-db"}},
		},
		{
			name: "same connector through two datastores applies once",
			rels: []Relationship{
				rel("b", KindContainer, "orders-api", KindDataStore, "audit-db"),
				rel("a", KindContainer, "orders-api", KindDataStore, "orders-db"),
				rel("c", KindContainer, "orders-api", KindDataStore, "cache"),
			},
			want: DataStoreUse{Connectors: []string{"postgres", "redis"}, Relationships: []string{"a", "b", "c"}},
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
