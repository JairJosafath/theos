package contract

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// TestEnvironmentsEnv is a TestEnvironments provider under test and the
// environments it leases.
type TestEnvironmentsEnv struct {
	Provider ports.TestEnvironments
	// Environment returns a fresh environment of the provider's member that
	// holds no lease yet.
	Environment func(t *testing.T) ports.Environment
	// Places counts the places (pods, machines) the provider holds for env.
	Places func(t *testing.T, env ports.Environment) int
	// Ready makes lease id's place ready, as the place itself would on a
	// real node or machine; nil for a provider whose leases are Ready at
	// once.
	Ready func(t *testing.T, env ports.Environment, id string)
	// Fail makes lease id's place fail, as a crashed environment would.
	Fail func(t *testing.T, env ports.Environment, id string)
}

// TestEnvironments states what every TestEnvironments provider does (T6):
// Lease twice with one ID is one place; a lease becomes Ready with an
// endpoint, or says it Failed with a message; Return frees its place, twice
// is no error, and Get then finds no lease; Leases lists the leases of its
// environment, with their holders, and none of another's.
func TestEnvironments(t *testing.T, env TestEnvironmentsEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	p := env.Provider
	request := func(e ports.Environment, run string) ports.LeaseRequest {
		return ports.LeaseRequest{ID: model.LeaseID(e.Name, run), Env: e, Holder: "add-crop",
			Token: "contract-" + run, Deadline: 10 * time.Minute}
	}
	lease := func(t *testing.T, req ports.LeaseRequest) ports.Lease {
		t.Helper()
		l, err := p.Lease(ctx, req)
		if err != nil {
			t.Fatalf("Lease(%s) = %v", req.ID, err)
		}
		if l.ID != req.ID {
			t.Fatalf("Lease(%s) gave lease %q", req.ID, l.ID)
		}
		return l
	}

	t.Run("leases one place per ID, however often Lease is called", func(t *testing.T) {
		e := env.Environment(t)
		req := request(e, "add-crop-r1-implement")
		first := lease(t, req)
		second := lease(t, req)
		if first.Started.IsZero() || !second.Started.Equal(first.Started) {
			t.Errorf("Started %v, then %v; want one start", first.Started, second.Started)
		}
		if n := env.Places(t, e); n != 1 {
			t.Errorf("Places = %d after two Leases with one ID, want 1", n)
		}
	})

	t.Run("a lease becomes Ready with an endpoint", func(t *testing.T) {
		e := env.Environment(t)
		req := request(e, "add-crop-r1-implement")
		lease(t, req)
		if env.Ready != nil {
			env.Ready(t, e, req.ID)
		}
		l, err := p.Get(ctx, e, req.ID)
		if err != nil {
			t.Fatalf("Get = %v", err)
		}
		if l.State != model.LeaseReady || l.Endpoint == "" || l.Holder != req.Holder {
			t.Errorf("Get = %+v, want Ready with an endpoint and holder %q", l, req.Holder)
		}
	})

	t.Run("a lease whose place failed says so", func(t *testing.T) {
		e := env.Environment(t)
		req := request(e, "add-crop-r1-implement")
		lease(t, req)
		env.Fail(t, e, req.ID)
		l, err := p.Get(ctx, e, req.ID)
		if err != nil {
			t.Fatalf("Get = %v", err)
		}
		if l.State != model.LeaseFailed || l.Message == "" {
			t.Errorf("Get = %+v, want Failed with a message", l)
		}
	})

	t.Run("returns a lease, and a second Return is no error", func(t *testing.T) {
		e := env.Environment(t)
		req := request(e, "add-crop-r1-implement")
		lease(t, req)
		for range 2 {
			if err := p.Return(ctx, e, req.ID); err != nil {
				t.Fatalf("Return = %v", err)
			}
		}
		if n := env.Places(t, e); n != 0 {
			t.Errorf("Places = %d after Return, want 0", n)
		}
		if _, err := p.Get(ctx, e, req.ID); !errors.Is(err, ports.ErrLeaseNotFound) {
			t.Errorf("Get after Return = %v, want ErrLeaseNotFound", err)
		}
	})

	t.Run("finds no lease it never made", func(t *testing.T) {
		if _, err := p.Get(ctx, env.Environment(t), "browser-0000000000"); !errors.Is(err, ports.ErrLeaseNotFound) {
			t.Errorf("Get = %v, want ErrLeaseNotFound", err)
		}
	})

	t.Run("lists the leases of its environment only, with their holders", func(t *testing.T) {
		e, other := env.Environment(t), env.Environment(t)
		a, b := request(e, "add-crop-r1-implement"), request(e, "add-crop-r1-verify")
		lease(t, a)
		lease(t, b)
		lease(t, request(other, "fix-rotate-r1-implement"))
		got, err := p.Leases(ctx, e)
		if err != nil {
			t.Fatalf("Leases = %v", err)
		}
		ids := make([]string, 0, len(got))
		for _, l := range got {
			ids = append(ids, l.ID)
			if l.Holder != "add-crop" {
				t.Errorf("lease %s has holder %q, want add-crop", l.ID, l.Holder)
			}
		}
		if want := sorted([]string{a.ID, b.ID}); !slices.Equal(sorted(ids), want) {
			t.Errorf("Leases = %v, want %v: the environment's two, the other's left out", sorted(ids), want)
		}
	})
}
