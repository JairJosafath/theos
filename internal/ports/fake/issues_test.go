package fake_test

import (
	"context"
	"slices"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// The fake Gitea hands out a copy of an issue's labels: what ListIssues and
// GetIssue returned stays as it was when the issue's labels change afterwards,
// as over HTTP, where a caller reading the labels and a reconciler relabeling the
// issue never shared a slice (#1820).
func TestIssuesHandOutACopyOfTheLabels(t *testing.T) {
	git := fake.New()
	provider := git
	repo := ports.Repository{Owner: "tools", Name: "cel-lens"}
	ctx := context.Background()
	if err := provider.EnsureLabels(ctx, repo, []ports.Label{{Name: model.LabelReady, Exclusive: true},
		{Name: model.LabelInProgress, Exclusive: true}, {Name: model.LabelBug}}); err != nil {
		t.Fatal(err)
	}
	n := git.AddIssue(repo.Owner, repo.Name, "The hover shows the rule", "", []string{model.LabelReady, model.LabelBug})
	want := []string{model.LabelReady, model.LabelBug}

	listed, err := provider.ListIssues(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !slices.Equal(listed[0].Labels, want) {
		t.Fatalf("ListIssues returned %v, want one issue labeled %v", listed, want)
	}
	got, err := provider.GetIssue(ctx, repo, n)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Labels, want) {
		t.Fatalf("GetIssue returned labels %v, want %v", got.Labels, want)
	}

	// Relabel the issue: a status label replaces the old one in place, and a
	// removal shifts what follows it.
	if err := provider.AddIssueLabel(ctx, repo, n, model.LabelInProgress); err != nil {
		t.Fatal(err)
	}
	if err := provider.RemoveIssueLabel(ctx, repo, n, model.LabelBug); err != nil {
		t.Fatal(err)
	}

	if now := git.Issue(repo.Owner, repo.Name, n).Labels; !slices.Equal(now, []string{model.LabelInProgress}) {
		t.Errorf("the issue's labels are %v, want %v", now, []string{model.LabelInProgress})
	}
	if !slices.Equal(listed[0].Labels, want) {
		t.Errorf("the labels ListIssues returned changed to %v, want still %v (#1820)", listed[0].Labels, want)
	}
	if !slices.Equal(got.Labels, want) {
		t.Errorf("the labels GetIssue returned changed to %v, want still %v (#1820)", got.Labels, want)
	}
}
