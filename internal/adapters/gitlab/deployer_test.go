package gitlab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JairJosafath/c4factory/internal/adapters/gitlab/gitlabtest"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/contract"
)

// projects numbers the fake's projects, one fresh per target.
var projects atomic.Int64

// newTarget adds a fresh project to gl, its own token "t0ken-N", and returns
// a target on it whose Secret holds token: the project's, or a wrong one.
func newTarget(gl *gitlabtest.Server, token string) (ports.Target, *gitlabtest.Project) {
	n := projects.Add(1)
	path := fmt.Sprintf("contract/orders-%d", n)
	p := gl.AddProject(path, fmt.Sprintf("t0ken-%d", n))
	if token == "t0ken" {
		token = fmt.Sprintf("t0ken-%d", n)
	}
	return ports.Target{Name: "contract/" + path, Member: model.DeployMemberGitLab,
		Settings: map[string]string{model.GitLabSettingURL: gl.URL(), model.GitLabSettingProject: path,
			model.GitLabSettingTagPrefix: "cli/"},
		Secret: map[string][]byte{model.GitLabSecretToken: []byte(token)}}, p
}

// marked counts the releases of p that the factory made for app.
func marked(p *gitlabtest.Project, app string) int {
	n := 0
	for _, r := range p.Releases() {
		if strings.Contains(r.Description, model.ShipMarkerPrefix(app)) {
			n++
		}
	}
	return n
}

func file(name, content string) ports.File {
	return ports.File{Name: name, Size: int64(len(content)),
		Open: func(context.Context) (io.ReadCloser, int64, error) {
			return io.NopCloser(strings.NewReader(content)), int64(len(content)), nil
		}}
}

// shipment is v1.0.0 of the app with files.
func shipment(files ...ports.File) ports.Shipment {
	return ports.Shipment{App: "acme/orders-cli", DisplayName: "Orders CLI", Version: "v1.0.0",
		Notes: "notes v1.0.0", Files: files}
}

// The gitlab member passes the Deployer contract against the fake GitLab:
// each target a fresh project, a wrong token refused, a deploy a marked
// release.
func TestDeployerContract(t *testing.T) {
	gl := gitlabtest.New(t)
	byPath := map[string]*gitlabtest.Project{}
	target := func(token string) ports.Target {
		tg, p := newTarget(gl, token)
		byPath[p.Path] = p
		return tg
	}
	contract.Deployer(t, contract.DeployerEnv{
		Deployer: New(nil),
		Target:   func(*testing.T) ports.Target { return target("t0ken") },
		Refused:  func(*testing.T) ports.Target { return target("wrong") },
		Deploys: func(_ *testing.T, tg ports.Target, app string) int {
			return marked(byPath[tg.Settings[model.GitLabSettingProject]], app)
		},
	})
}

// A token without the api scope can't make a release: Check refuses the
// credential, naming the scope (docs/plans/gitlab-deploy.md, Decision 2).
func TestCheckScopes(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	p.Scopes = []string{"read_api", "write_registry"}
	err := New(nil).Check(context.Background(), tg)
	if !errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), "api scope") {
		t.Fatalf("Check() = %v, want ErrCredentialRefused naming the api scope", err)
	}
}

// A release of the tag that the factory didn't make is left alone: Ship fails
// with reason TagTaken, neither unreachable nor a refused credential, and
// nothing is written.
func TestShipTagTaken(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	p.AddRelease("cli/v1.0.0", "someone else's release")
	err := New(nil).Ship(context.Background(), tg, shipment(file("orders-cli_linux.tar.gz", "bin")))
	re, ok := errors.AsType[*gitprovider.ReleaseError](err)
	if !ok || re.Reason != model.PublishTagTaken {
		t.Fatalf("Ship() = %v, want a ReleaseError with reason TagTaken", err)
	}
	if errors.Is(err, ports.ErrUnreachable) || errors.Is(err, ports.ErrCredentialRefused) {
		t.Fatalf("Ship() = %v, want no port error", err)
	}
	if n := len(p.Files("orders-cli", "v1.0.0")); n != 0 {
		t.Errorf("files uploaded = %d, want 0", n)
	}
}

// A package file of another size than the release's is deleted and uploaded
// again before the release is made, and a release half shipped is Deploying.
func TestShipReplacesFile(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	d := New(nil)
	if err := d.Ship(ctx, tg, shipment(file("orders-cli_linux.tar.gz", "short"), file("SHA256SUMS", "s"))); err != nil {
		t.Fatalf("Ship() = %v", err)
	}
	if got := p.Files("orders-cli", "v1.0.0"); got["orders-cli_linux.tar.gz"] != 5 {
		t.Fatalf("files after the first ship = %v, want the binary at 5 bytes", got)
	}
	state, err := d.State(ctx, tg, "acme/orders-cli", "v1.0.0")
	if err != nil || state.Phase != ports.PhaseDeploying || state.Version != "v1.0.0" {
		t.Fatalf("State() mid-ship = %+v, %v, want Deploying v1.0.0", state, err)
	}
	// CI rebuilt the file: it comes back longer.
	s := shipment(file("orders-cli_linux.tar.gz", "a longer binary"), file("SHA256SUMS", "s"))
	for i := 0; i < 3 && marked(p, s.App) == 0; i++ {
		if err := d.Ship(ctx, tg, s); err != nil {
			t.Fatalf("Ship() %d = %v", i, err)
		}
	}
	if got := p.Files("orders-cli", "v1.0.0"); len(got) != 2 || got["orders-cli_linux.tar.gz"] != 15 {
		t.Fatalf("files = %v, want the binary replaced at 15 bytes and the sums", got)
	}
	rels := p.Releases()
	if len(rels) != 1 || rels[0].Tag != "cli/v1.0.0" || len(rels[0].Links) != 2 {
		t.Fatalf("releases = %+v, want one of cli/v1.0.0 with two links", rels)
	}
	if l := rels[0].Links[0]; l.LinkType != "package" || !strings.HasSuffix(l.URL,
		"/packages/generic/orders-cli/v1.0.0/orders-cli_linux.tar.gz") || l.DirectAssetPath != "/orders-cli_linux.tar.gz" {
		t.Errorf("link = %+v, want a package link to the generic download URL", l)
	}
	state, err = d.State(ctx, tg, "acme/orders-cli", "")
	if err != nil || state.Phase != ports.PhaseDeployed || state.Version != "v1.0.0" || state.URL == "" {
		t.Fatalf("State() = %+v, %v, want Deployed v1.0.0 at its page", state, err)
	}
}

// The package registry being off fails the entry with GitLab's message, not
// unreachable, so it isn't retried forever.
func TestShipPackagesOff(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	p.PackagesEnabled = false
	err := New(nil).Ship(context.Background(), tg, shipment(file("orders-cli_linux.tar.gz", "bin")))
	re, ok := errors.AsType[*gitprovider.ReleaseError](err)
	if !ok || re.Reason != gitprovider.ReasonGitLabRejected || errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("Ship() = %v, want GitLabRejected, not a port error", err)
	}
}
