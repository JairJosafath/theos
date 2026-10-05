package controller

import (
	"context"
	"errors"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The fake host's releases: no controller tags yet, so these fail if called.

var errNoFakeReleases = errors.New("the fake host has no releases")

func (p *fakeProvider) ListMilestones(context.Context, gitprovider.Repository) ([]gitprovider.Milestone, error) {
	return nil, errNoFakeReleases
}

func (p *fakeProvider) MilestoneIssues(context.Context, gitprovider.Repository, int64) (open, closed []int, err error) {
	return nil, nil, errNoFakeReleases
}

func (p *fakeProvider) CloseMilestone(context.Context, gitprovider.Repository, int64) error {
	return errNoFakeReleases
}

func (p *fakeProvider) GetTag(context.Context, gitprovider.Repository, string) (string, error) {
	return "", errNoFakeReleases
}

func (p *fakeProvider) CreateTag(context.Context, gitprovider.Repository, string, string, string) error {
	return errNoFakeReleases
}
