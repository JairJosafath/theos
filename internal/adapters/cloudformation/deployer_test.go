package cloudformation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/smithy-go"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/contract"
)

const (
	testRegion = "eu-west-1"
	testApp    = "webshop/price-fn"
)

// fixtureFiles are the release files of a Framework's fixture, by flat
// name, each streamed through a reader that can't seek.
func fixtureFiles(t testing.TB, framework string) []ports.File {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", framework, "*"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixture of %s: %v", framework, err)
	}
	out := make([]ports.File, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ports.File{Name: filepath.Base(p), Size: int64(len(b)),
			Open: func(context.Context) (io.ReadCloser, int64, error) {
				return io.NopCloser(struct{ io.Reader }{bytes.NewReader(b)}), int64(len(b)), nil
			}})
	}
	return out
}

// world is a fake AWS, the Deployer on it, and how often it slept.
type world struct {
	aws    *fakeAWS
	d      *Deployer
	sleeps int
}

func newWorld(t testing.TB) *world {
	t.Helper()
	_, version, err := bootstrapTemplate()
	if err != nil {
		t.Fatal(err)
	}
	w := &world{aws: newFakeAWS(version)}
	w.d = New(w.aws.clients)
	w.d.sleep = func(context.Context, time.Duration) error { w.sleeps++; return nil }
	return w
}

// account adds an account and returns it with a target of tier on its
// user's key.
func (w *world) account(key, tier string, mayBootstrap bool) (*fakeAccount, ports.Target) {
	return w.aws.account(key, mayBootstrap), target(key, tier)
}

// bootstrapped adds an account that runs the current bootstrap, and a
// production target of it.
func (w *world) bootstrapped() (*fakeAccount, ports.Target) {
	a, t := w.account("AKIAUSER", "production", false)
	w.aws.bootstrap(a, w.aws.bootstrapVersion, map[string]string{executionPoliciesParam: "arn:aws:iam::aws:policy/AdministratorAccess"})
	return a, t
}

func target(key, tier string) ports.Target {
	return ports.Target{Name: "webshop/aws-" + tier, Member: "cloudFormation", Tier: tier,
		Settings: map[string]string{model.CloudFormationSettingRegion: testRegion},
		Secret:   map[string][]byte{accessKeyIDKey: []byte(key), secretAccessKeyKey: []byte("secret")}}
}

func shipment(framework, version string, files []ports.File) ports.Shipment {
	return ports.Shipment{App: testApp, DisplayName: framework, Version: version, Files: files}
}

// testImage is a release's image in the Registry, by digest.
const testImage = "gitea.example/webshop/checkout@sha256:" +
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// withImage is s with testImage, copied into w's fake ECR.
func (w *world) withImage(s ports.Shipment) ports.Shipment {
	s.Image, s.CopyImage = testImage, w.aws.copyImage(testImage)
	return s
}

// shipUntilDeployed ships s until State reports it Deployed, and returns
// that state.
func shipUntilDeployed(t *testing.T, d *Deployer, target ports.Target, s ports.Shipment) ports.DeployState {
	t.Helper()
	ctx := context.Background()
	for range 20 {
		if err := d.Ship(ctx, target, s); err != nil {
			t.Fatalf("Ship(%s) = %v", s.Version, err)
		}
		st, err := d.State(ctx, target, s.App, s.Version)
		if err != nil {
			t.Fatalf("State() = %v", err)
		}
		if st.Version == s.Version && st.Phase == ports.PhaseDeployed {
			return st
		}
	}
	t.Fatalf("%s isn't Deployed after 20 Ship calls", s.Version)
	return ports.DeployState{}
}

func (a *fakeAccount) appExecutions(app string) int {
	n := 0
	for _, e := range a.executions {
		if e.App == app {
			n++
		}
	}
	return n
}

// appChangeSets are the change sets made of stacks other than CDKToolkit.
func (a *fakeAccount) appChangeSets() []fakeChangeSetCall {
	var out []fakeChangeSetCall
	for _, c := range a.changeSets {
		if aws.ToString(c.In.StackName) != toolkitStack {
			out = append(out, c)
		}
	}
	return out
}

func role(a *fakeAccount, kind string) string {
	return "arn:aws:iam::" + a.id + ":role/cdk-hnb659fds-" + kind + "-role-" + a.id + "-" + testRegion
}

func TestDeployerContract(t *testing.T) {
	w := newWorld(t)
	accounts := map[string]*fakeAccount{}
	keys := 0
	newKey := func() string { keys++; return "AKIA" + strings.Repeat("0", 3) + string(rune('A'+keys)) }
	files := fixtureFiles(t, "aws-lambda-ts")
	contract.Deployer(t, contract.DeployerEnv{
		Deployer: w.d,
		Target: func(*testing.T) ports.Target {
			key := newKey()
			a, tt := w.account(key, "production", true)
			accounts[key] = a
			return tt
		},
		Refused: func(*testing.T) ports.Target { return target("AKIA-NOT-THERE", "production") },
		Unprepared: func(*testing.T) ports.Target {
			key := newKey()
			a, tt := w.account(key, "production", false)
			accounts[key] = a
			return tt
		},
		Deploys: func(t *testing.T, tt ports.Target, app string) int {
			return accounts[string(tt.Secret[accessKeyIDKey])].appExecutions(app)
		},
		Files: func(string) []ports.File { return files },
	})
}

// Each of the five Frameworks' assemblies ships to Deployed at both tiers,
// bootstrap first, and shipping it again starts nothing (#1608); Fargate's
// with its image (#1610).
func TestShipEachFramework(t *testing.T) {
	cases := map[string]struct {
		stack, url string
		image      bool
	}{
		"aws-lambda-ts":  {stack: "price-fn", url: "http://price-fn"},
		"aws-lambda-go":  {stack: "stock-fn", url: "http://stock-fn"},
		"aws-lambda-py":  {stack: "tax-fn", url: "http://tax-fn"},
		"aws-ec2-ts":     {stack: "search", url: "https://search"},
		"aws-fargate-ts": {stack: "checkout", url: "https://checkout", image: true},
	}
	for framework, c := range cases {
		files := fixtureFiles(t, framework)
		for _, tier := range []string{"development", "production"} {
			t.Run(framework+"/"+tier, func(t *testing.T) {
				w := newWorld(t)
				a, tt := w.account("AKIAUSER", tier, true)
				tt.Parameters = map[string]string{"CertificateArn": "arn:aws:acm:eu-west-1:1:certificate/x",
					"Unused": "ignored"}
				stack := c.stack
				if tier == "development" {
					stack += "-development"
				}
				s := shipment(framework, "v1.0.0", files)
				if c.image {
					s = w.withImage(s)
				}
				st := shipUntilDeployed(t, w.d, tt, s)
				if want := strings.Replace(c.url, c.stack, stack, 1) + ".example"; st.URL != want ||
					st.Health != ports.HealthHealthy {
					t.Errorf("State() = %+v, want URL %s and Healthy", st, want)
				}
				if !a.bootstrapped() {
					t.Error("CDKToolkit isn't there after the first deploy")
				}
				sets := a.appChangeSets()
				if len(sets) != 1 || aws.ToString(sets[0].In.StackName) != stack {
					t.Fatalf("change sets = %+v, want one of %s", sets, stack)
				}
				for _, p := range sets[0].In.Parameters {
					if aws.ToString(p.ParameterKey) == "Unused" {
						t.Error("a parameter the template doesn't declare was passed")
					}
				}
				if got := a.stacks[stack].protected; got != (tier == "production") {
					t.Errorf("termination protection = %v at %s", got, tier)
				}
				puts := len(a.puts)
				for range 3 {
					if err := w.d.Ship(context.Background(), tt, s); err != nil {
						t.Fatalf("Ship() again = %v", err)
					}
				}
				if n := a.appExecutions(testApp); n != 1 || len(a.appChangeSets()) != 1 || len(a.puts) != puts {
					t.Errorf("after shipping again: %d executions, %d change sets, %d puts; want 1, 1, %d", n,
						len(a.appChangeSets()), len(a.puts), puts)
				}
				if c.image != (len(a.copies) == 1) {
					t.Errorf("image copies = %v, want one only when the stack runs an image", a.copies)
				}
			})
		}
	}
}

// Publishing puts only the assets S3 lacks, as the file-publishing role,
// and the template is one of them (#1608).
func TestShipPublishesMissingAssets(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-lambda-ts")
	bucket := "cdk-hnb659fds-assets-" + a.id + "-" + testRegion
	template := bucket + "/036f40f366040a2a7a1eba24cd658fd9912e92d24437b8216b6f47dd8c498d55.json"
	a.objects[template] = []byte("there already")
	if err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", files)); err != nil {
		t.Fatal(err)
	}
	zip := bucket + "/2ab6994d9e7bc0d45f63553f0bcecd425c25b142dab3ce3cd794b5fce299c553.zip"
	if len(a.puts) != 1 || a.puts[0].What != zip || a.puts[0].Caller != role(a, "file-publishing") {
		t.Fatalf("puts = %+v, want only %s by %s", a.puts, zip, role(a, "file-publishing"))
	}
	want, _ := os.ReadFile("testdata/aws-lambda-ts/asset.2ab6994d9e7bc0d45f63553f0bcecd425c25b142dab3ce3cd794b5fce299c553.zip")
	if !bytes.Equal(a.objects[zip], want) {
		t.Error("the published asset isn't the release file")
	}

	// A fresh account gets the template too.
	w2 := newWorld(t)
	b, tt2 := w2.bootstrapped()
	if err := w2.d.Ship(context.Background(), tt2, shipment("aws-lambda-ts", "v1.0.0", files)); err != nil {
		t.Fatal(err)
	}
	if len(b.puts) != 2 {
		t.Errorf("puts = %+v, want the zip and the template", b.puts)
	}
}

// The change set: CREATE then UPDATE, the template's URL, the cfn-exec
// role, the three capabilities, the parameters and tags, made by the
// deploy role (#1608).
func TestShipChangeSet(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-lambda-ts")
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.0.0", files))
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.1.0", files))
	sets := a.appChangeSets()
	if len(sets) != 2 {
		t.Fatalf("change sets = %d, want 2", len(sets))
	}
	for i, typ := range []types.ChangeSetType{types.ChangeSetTypeCreate, types.ChangeSetTypeUpdate} {
		in, caller := sets[i].In, sets[i].Caller
		if in.ChangeSetType != typ {
			t.Errorf("change set %d type = %s, want %s", i, in.ChangeSetType, typ)
		}
		if caller != role(a, "deploy") {
			t.Errorf("change set %d made by %s, want the deploy role", i, caller)
		}
	}
	in := sets[1].In
	wantURL := "https://cdk-hnb659fds-assets-" + a.id + "-" + testRegion + ".s3." + testRegion +
		".amazonaws.com/036f40f366040a2a7a1eba24cd658fd9912e92d24437b8216b6f47dd8c498d55.json"
	if aws.ToString(in.TemplateURL) != wantURL || in.TemplateBody != nil {
		t.Errorf("TemplateURL = %s, want %s", aws.ToString(in.TemplateURL), wantURL)
	}
	if got := aws.ToString(in.RoleARN); got != role(a, "cfn-exec") {
		t.Errorf("RoleARN = %s, want %s", got, role(a, "cfn-exec"))
	}
	if !slices.Equal(in.Capabilities, capabilities) || len(capabilities) != 3 {
		t.Errorf("Capabilities = %v", in.Capabilities)
	}
	if aws.ToString(in.ChangeSetName) != "c4factory-v1-2e1-2e0" {
		t.Errorf("ChangeSetName = %s", aws.ToString(in.ChangeSetName))
	}
	tags := map[string]string{}
	for _, tag := range in.Tags {
		tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	for k, v := range map[string]string{model.CloudFormationTagApp: testApp, model.CloudFormationTagVersion: "v1.1.0",
		model.CloudFormationTagTier: "production", "c4factory:container": "price-fn"} {
		if tags[k] != v {
			t.Errorf("tag %s = %q, want %q", k, tags[k], v)
		}
	}
	if w.sleeps != len(sets) {
		t.Errorf("slept %d times; the fake's change sets are ready on the second read", w.sleeps)
	}
}

// Each execution's ClientRequestToken carries its version and a suffix of
// its change set's id, so shipping a version again is told apart (#1608).
func TestShipToken(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-lambda-ts")
	for _, v := range []string{"v1.0.0", "v1.1.0", "v1.0.0"} {
		shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", v, files))
	}
	var tokens []string
	for _, e := range a.executions {
		if e.App != testApp {
			continue
		}
		tokens = append(tokens, e.Token)
		if e.Caller != role(a, "deploy") {
			t.Errorf("executed by %s, want the deploy role", e.Caller)
		}
	}
	if len(tokens) != 3 {
		t.Fatalf("tokens = %v, want 3", tokens)
	}
	for i, want := range []string{"v1.0.0", "v1.1.0", "v1.0.0"} {
		if v, ok := model.CDKTokenVersion(tokens[i]); !ok || v != want {
			t.Errorf("token %s carries %q, %v; want %s", tokens[i], v, ok, want)
		}
	}
	if tokens[0] == tokens[2] {
		t.Errorf("shipping v1.0.0 again reused its token %s", tokens[0])
	}
	if got := tokenSuffix("arn:aws:cloudformation:eu-west-1:1:changeSet/x/1"); len(got) != 8 {
		t.Errorf("tokenSuffix = %q, want 8 hex characters", got)
	}
}

// An update that rolled back reports its version Failed, reason RolledBack,
// not the version still running (#1608).
func TestStateRolledBack(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-lambda-ts")
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.0.0", files))
	a.fail["price-fn"] = true
	ctx := context.Background()
	if err := w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.1.0", files)); err != nil {
		t.Fatal(err)
	}
	st, err := w.d.State(ctx, tt, testApp, "v1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "v1.1.0" || st.Phase != ports.PhaseFailed || st.Reason != model.DeployReasonRolledBack ||
		st.Health != ports.HealthDegraded || !strings.Contains(st.Message, "failed to update") {
		t.Errorf("State() = %+v, want v1.1.0 Failed RolledBack", st)
	}
	// The version that ran before ships again as a new deploy.
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.0.0", files))
}

// A first create that rolled back can't be updated: Ship says to delete the
// stack, a failure, and deletes nothing (#1608).
func TestShipRollbackComplete(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-lambda-ts")
	a.fail["price-fn"] = true
	ctx := context.Background()
	if err := w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.0.0", files)); err != nil {
		t.Fatal(err)
	}
	st, _ := w.d.State(ctx, tt, testApp, "v1.0.0")
	if st.Phase != ports.PhaseFailed || st.Version != "v1.0.0" {
		t.Errorf("State() = %+v, want v1.0.0 Failed", st)
	}
	err := w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.0.1", files))
	if err == nil || !strings.Contains(err.Error(), "delete the stack") || isPortError(err) {
		t.Errorf("Ship() = %v, want a plain error saying to delete the stack", err)
	}
	if a.stacks["price-fn"] == nil {
		t.Error("the stack is gone")
	}
}

func isPortError(err error) bool {
	return errors.Is(err, ports.ErrCredentialRefused) || errors.Is(err, ports.ErrUnreachable) ||
		errors.Is(err, ports.ErrPrerequisiteMissing) || errors.Is(err, ports.ErrArtifactMissing)
}

// A stack tagged for another app is left alone: StackTaken, a plain
// failure (#1608).
func TestShipStackTaken(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	a.stacks["price-fn"] = &fakeStack{name: "price-fn", id: "stack/price-fn/1", status: "UPDATE_COMPLETE",
		tags: map[string]string{model.CloudFormationTagApp: "other/price-fn"}, changeSets: map[string]*fakeChangeSet{}}
	err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", fixtureFiles(t, "aws-lambda-ts")))
	if !errors.Is(err, ErrStackTaken) || isPortError(err) || !strings.Contains(err.Error(), "other/price-fn") {
		t.Errorf("Ship() = %v, want StackTaken naming the owner", err)
	}
	if len(a.changeSets) != 0 || len(a.puts) != 0 {
		t.Errorf("Ship touched a taken stack: %d change sets, %d puts", len(a.changeSets), len(a.puts))
	}
	st, err := w.d.State(context.Background(), tt, testApp, "")
	if err != nil || st.Phase != "" {
		t.Errorf("State() = %+v, %v; want the zero state, the stack being another app's", st, err)
	}
}

// The bootstrap is created from the embedded template when the account has
// none; Ship waits for it, then ships (#1608).
func TestShipBootstrapCreated(t *testing.T) {
	w := newWorld(t)
	a, tt := w.account("AKIAUSER", "production", true)
	files := fixtureFiles(t, "aws-lambda-ts")
	ctx := context.Background()
	if err := w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.0.0", files)); err != nil {
		t.Fatal(err)
	}
	body, version, _ := bootstrapTemplate()
	if len(a.changeSets) != 1 {
		t.Fatalf("change sets = %+v, want the bootstrap's", a.changeSets)
	}
	in, caller := a.changeSets[0].In, a.changeSets[0].Caller
	if aws.ToString(in.StackName) != toolkitStack || in.ChangeSetType != types.ChangeSetTypeCreate ||
		aws.ToString(in.TemplateBody) != body || !strings.HasSuffix(caller, ":user/deployer") {
		t.Errorf("bootstrap change set = %s %s by %s", aws.ToString(in.StackName), in.ChangeSetType, caller)
	}
	if got := params(in.Parameters); got[executionPoliciesParam] != "arn:aws:iam::aws:policy/AdministratorAccess" {
		t.Errorf("bootstrap parameters = %v", got)
	}
	if len(a.puts) != 0 || len(a.appChangeSets()) != 0 {
		t.Error("Ship went on before the bootstrap finished")
	}
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.0.0", files))
	if a.params[defaultBootstrapParam] != strconv.Itoa(version) {
		t.Errorf("bootstrap version = %s, want %d", a.params[defaultBootstrapParam], version)
	}
}

// An outdated bootstrap is updated, keeping its parameters, a scoped
// execution policy above all (#1608).
func TestShipBootstrapOutdated(t *testing.T) {
	w := newWorld(t)
	a, tt := w.account("AKIAUSER", "production", true)
	scoped := "arn:aws:iam::" + a.id + ":policy/scoped"
	w.aws.bootstrap(a, 5, map[string]string{executionPoliciesParam: scoped, "Qualifier": "hnb659fds",
		"RetiredParameter": "x"})
	files := fixtureFiles(t, "aws-lambda-ts")
	if err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", files)); err != nil {
		t.Fatal(err)
	}
	if len(a.changeSets) != 1 || a.changeSets[0].In.ChangeSetType != types.ChangeSetTypeUpdate {
		t.Fatalf("change sets = %+v, want an UPDATE of CDKToolkit", a.changeSets)
	}
	kept := make([]string, 0, len(a.changeSets[0].In.Parameters))
	for _, p := range a.changeSets[0].In.Parameters {
		if !aws.ToBool(p.UsePreviousValue) || p.ParameterValue != nil {
			t.Errorf("parameter %s isn't kept", aws.ToString(p.ParameterKey))
		}
		kept = append(kept, aws.ToString(p.ParameterKey))
	}
	// A parameter the embedded template no longer declares isn't passed:
	// CloudFormation refuses the change set over it.
	if want := []string{executionPoliciesParam, "Qualifier"}; !slices.Equal(kept, want) {
		t.Errorf("kept parameters = %v, want %v", kept, want)
	}
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.0.0", files))
	if got := a.stacks[toolkitStack].params[executionPoliciesParam]; got != scoped {
		t.Errorf("execution policy = %s, want the scoped one kept", got)
	}
}

// A credential that may not bootstrap, or a CDKToolkit that failed, is
// ErrPrerequisiteMissing with the command the account's owner runs (#1608).
func TestShipBootstrapRefused(t *testing.T) {
	files := fixtureFiles(t, "aws-lambda-ts")
	ctx := context.Background()
	t.Run("may not", func(t *testing.T) {
		w := newWorld(t)
		a, tt := w.account("AKIAUSER", "production", false)
		err := w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.0.0", files))
		if !errors.Is(err, ports.ErrPrerequisiteMissing) ||
			!strings.Contains(err.Error(), "cdk bootstrap aws://"+a.id+"/"+testRegion) {
			t.Errorf("Ship() = %v, want ErrPrerequisiteMissing with the cdk bootstrap command", err)
		}
	})
	for _, status := range []string{"UPDATE_ROLLBACK_FAILED", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED",
		"UPDATE_FAILED", "DELETE_FAILED"} {
		t.Run("failed "+status, func(t *testing.T) {
			w := newWorld(t)
			a, tt := w.account("AKIAUSER", "production", true)
			w.aws.bootstrap(a, 5, nil)
			a.stacks[toolkitStack].status = status
			err := w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.0.0", files))
			if !errors.Is(err, ports.ErrPrerequisiteMissing) || !strings.Contains(err.Error(), status) {
				t.Errorf("Ship() = %v, want ErrPrerequisiteMissing naming the status", err)
			}
		})
	}
}

// A CDKToolkit whose last update rolled back runs its old version and takes
// an update, not a person (#1608).
func TestShipBootstrapAfterRolledBackUpdate(t *testing.T) {
	w := newWorld(t)
	a, tt := w.account("AKIAUSER", "production", true)
	w.aws.bootstrap(a, 5, map[string]string{executionPoliciesParam: "arn:aws:iam::aws:policy/AdministratorAccess"})
	a.stacks[toolkitStack].status = "UPDATE_ROLLBACK_COMPLETE"
	files := fixtureFiles(t, "aws-lambda-ts")
	if err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", files)); err != nil {
		t.Fatalf("Ship() = %v, want the bootstrap updated", err)
	}
	if len(a.changeSets) != 1 || a.changeSets[0].In.ChangeSetType != types.ChangeSetTypeUpdate {
		t.Fatalf("change sets = %+v, want an UPDATE of CDKToolkit", a.changeSets)
	}
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.0.0", files))
}

// The factory's own update of CDKToolkit that rolled back is
// ErrPrerequisiteMissing, not taken again on every poll (#1608).
func TestShipBootstrapOwnUpdateRolledBack(t *testing.T) {
	w := newWorld(t)
	a, tt := w.account("AKIAUSER", "production", true)
	w.aws.bootstrap(a, 5, map[string]string{executionPoliciesParam: "arn:aws:iam::aws:policy/AdministratorAccess"})
	st := a.stacks[toolkitStack]
	st.status = "UPDATE_ROLLBACK_COMPLETE"
	_, version, err := bootstrapTemplate()
	if err != nil {
		t.Fatal(err)
	}
	st.events = append(st.events, stackEvent(st, st.status, bootstrapToken(version)+"0123abcd"))
	err = w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", fixtureFiles(t, "aws-lambda-ts")))
	if !errors.Is(err, ports.ErrPrerequisiteMissing) || !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("Ship() = %v, want ErrPrerequisiteMissing saying the update rolled back", err)
	}
	if len(a.changeSets) != 0 {
		t.Errorf("change sets = %+v, want none", a.changeSets)
	}
}

// A template parameter with no default that the target doesn't give is
// ErrPrerequisiteMissing naming it, before anything is published; given, it
// reaches the change set (#1608).
func TestShipParameters(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-ec2-ts")
	ctx := context.Background()
	err := w.d.Ship(ctx, tt, shipment("aws-ec2-ts", "v1.0.0", files))
	if !errors.Is(err, ports.ErrPrerequisiteMissing) || !strings.Contains(err.Error(), "CertificateArn") {
		t.Errorf("Ship() = %v, want ErrPrerequisiteMissing naming CertificateArn", err)
	}
	if len(a.puts) != 0 || len(a.changeSets) != 0 {
		t.Error("Ship published or made a change set without the parameter")
	}
	cert := "arn:aws:acm:eu-west-1:" + a.id + ":certificate/abc"
	tt.Parameters = map[string]string{"CertificateArn": cert}
	st := shipUntilDeployed(t, w.d, tt, shipment("aws-ec2-ts", "v1.0.0", files))
	if got := params(a.appChangeSets()[0].In.Parameters); len(got) != 1 || got["CertificateArn"] != cert {
		t.Errorf("change set parameters = %v", got)
	}
	if st.URL != "https://search.example" {
		t.Errorf("URL = %s, want https:// for a stack with a certificate", st.URL)
	}
}

// An empty change set is deleted and counts as deployed: nothing executes.
func TestShipEmptyChangeSet(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-lambda-ts")
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.0.0", files))
	a.empty["price-fn"] = true
	if err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.1", files)); err != nil {
		t.Fatal(err)
	}
	if n := a.appExecutions(testApp); n != 1 || len(a.stacks["price-fn"].changeSets) != 0 {
		t.Errorf("executions = %d, change sets left = %d; want 1 and 0", n, len(a.stacks["price-fn"].changeSets))
	}
}

// A release without its assembly's files is ErrArtifactMissing.
func TestShipArtifactMissing(t *testing.T) {
	w := newWorld(t)
	_, tt := w.bootstrapped()
	files := slices.DeleteFunc(fixtureFiles(t, "aws-lambda-ts"), func(f ports.File) bool {
		return strings.HasPrefix(f.Name, "asset.")
	})
	err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", files))
	if !errors.Is(err, ports.ErrArtifactMissing) || !strings.Contains(err.Error(), "asset.2ab6") {
		t.Errorf("Ship() = %v, want ErrArtifactMissing naming the asset", err)
	}
	tt.Tier = "staging"
	if err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", files)); !errors.Is(err,
		ports.ErrArtifactMissing) {
		t.Errorf("Ship() of a tier the release lacks = %v, want ErrArtifactMissing", err)
	}
}

// A stack that runs the release's image gets it copied by digest into the
// bootstrap's ECR repository, as the image-publishing role, and its
// ImageUri names the copy; a release whose image is there copies nothing,
// and a release without one is ErrArtifactMissing before anything is
// published (#1610).
func TestShipImage(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-fargate-ts")
	tt.Parameters = map[string]string{"CertificateArn": "arn:aws:acm:eu-west-1:1:certificate/x"}

	err := w.d.Ship(context.Background(), tt, shipment("aws-fargate-ts", "v1.0.0", files))
	if !errors.Is(err, ports.ErrArtifactMissing) || !strings.Contains(err.Error(), "checkout") {
		t.Errorf("Ship() without an image = %v, want ErrArtifactMissing naming the stack", err)
	}
	if len(a.puts) != 0 || len(a.appChangeSets()) != 0 {
		t.Fatalf("without an image: %d puts, %d change sets; want none", len(a.puts), len(a.appChangeSets()))
	}

	shipUntilDeployed(t, w.d, tt, w.withImage(shipment("aws-fargate-ts", "v1.0.0", files)))
	repo := "cdk-hnb659fds-container-assets-" + a.id + "-" + testRegion
	want := a.id + ".dkr.ecr." + testRegion + ".amazonaws.com/" + repo + "@" + strings.TrimPrefix(testImage,
		"gitea.example/webshop/checkout@")
	if len(a.copies) != 1 || a.copies[0].What != want {
		t.Errorf("copies = %+v, want one to %s", a.copies, want)
	}
	sets := a.appChangeSets()
	if len(sets) != 1 || params(sets[0].In.Parameters)["ImageUri"] != want {
		t.Fatalf("change sets = %+v, want one with ImageUri %s", sets, want)
	}

	shipUntilDeployed(t, w.d, tt, w.withImage(shipment("aws-fargate-ts", "v1.0.1", files)))
	if len(a.copies) != 1 {
		t.Errorf("copies after a release of the same image = %d, want 1", len(a.copies))
	}
}

// TestShipImageReadRefused (#1877): a registry that refuses the tag's read
// refuses the credential, with the read the credential lacks named, and the
// deploy is not retried as unreachable; nothing is published.
func TestShipImageReadRefused(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-fargate-ts")
	tt.Parameters = map[string]string{"CertificateArn": "arn:aws:acm:eu-west-1:1:certificate/x"}
	s := w.withImage(shipment("aws-fargate-ts", "v1.0.0", files))
	s.CopyImage = func(context.Context, ports.ImageRegistry, string, string) error {
		return fmt.Errorf("gitea: %w: %s answered 403 Forbidden to the tag's read; it must read tags as well as "+
			"push: %w", oci.ErrReadRefused, "1.dkr.ecr.eu-west-1.amazonaws.com/assets:0123",
			&transport.Error{StatusCode: http.StatusForbidden})
	}

	err := w.d.Ship(context.Background(), tt, s)
	if !errors.Is(err, ports.ErrCredentialRefused) || errors.Is(err, ports.ErrUnreachable) ||
		!strings.Contains(err.Error(), "must read tags as well as push") {
		t.Errorf("Ship() = %v, want ErrCredentialRefused, not unreachable, naming the read the credential lacks", err)
	}
	if len(a.copies) != 0 || len(a.appChangeSets()) != 0 {
		t.Errorf("after the refused read: %d copies, %d change sets; want none", len(a.copies),
			len(a.appChangeSets()))
	}
}

func TestCheck(t *testing.T) {
	w := newWorld(t)
	a, tt := w.account("AKIAUSER", "production", false)
	ctx := context.Background()
	if err := w.d.Check(ctx, tt); err != nil {
		t.Errorf("Check() = %v", err)
	}
	tt.Settings[model.CloudFormationSettingAccount] = a.id
	if err := w.d.Check(ctx, tt); err != nil {
		t.Errorf("Check() with the right account = %v", err)
	}
	tt.Settings[model.CloudFormationSettingAccount] = "999999999999"
	err := w.d.Check(ctx, tt)
	if !errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), a.id) ||
		!strings.Contains(err.Error(), "999999999999") {
		t.Errorf("Check() of another account = %v, want ErrCredentialRefused naming both", err)
	}
	err = w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.0.0", fixtureFiles(t, "aws-lambda-ts")))
	if !errors.Is(err, ports.ErrCredentialRefused) || len(a.changeSets) != 0 {
		t.Errorf("Ship() to another account = %v, want ErrCredentialRefused and nothing made", err)
	}
	delete(tt.Secret, secretAccessKeyKey)
	if err := w.d.Check(ctx, tt); !errors.Is(err, ports.ErrCredentialRefused) {
		t.Errorf("Check() without a secret key = %v, want ErrCredentialRefused", err)
	}
	w.aws.down = true
	if err := w.d.Check(ctx, target("AKIAUSER", "production")); !errors.Is(err, ports.ErrUnreachable) {
		t.Errorf("Check() with AWS down = %v, want ErrUnreachable", err)
	}
	if _, err := w.d.State(ctx, target("AKIAUSER", "production"), testApp, ""); !errors.Is(err, ports.ErrUnreachable) {
		t.Errorf("State() with AWS down = %v, want ErrUnreachable", err)
	}
}

type statusErr struct{ status int }

func (e statusErr) Error() string       { return "http " + strconv.Itoa(e.status) }
func (e statusErr) HTTPStatusCode() int { return e.status }

type apiStatusErr struct {
	statusErr
	code string
}

func (e apiStatusErr) ErrorCode() string             { return e.code }
func (e apiStatusErr) ErrorMessage() string          { return e.code }
func (e apiStatusErr) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

func TestPortError(t *testing.T) {
	for _, c := range []struct {
		err  error
		want error
	}{
		{apiErr("InvalidClientTokenId", ""), ports.ErrCredentialRefused},
		{apiErr("SignatureDoesNotMatch", ""), ports.ErrCredentialRefused},
		{apiErr("ExpiredToken", ""), ports.ErrCredentialRefused},
		{apiErr("AccessDenied", ""), ports.ErrCredentialRefused},
		{apiErr("AccessDeniedException", ""), ports.ErrCredentialRefused},
		{apiErr("Throttling", ""), ports.ErrUnreachable},
		{apiStatusErr{statusErr{503}, "Whatever"}, ports.ErrUnreachable},
		{errors.New("dial tcp: i/o timeout"), ports.ErrUnreachable},
		{apiErr("ValidationError", "Template format error"), nil},
		{apiStatusErr{statusErr{400}, "Whatever"}, nil},
	} {
		got := portError(c.err)
		if c.want == nil {
			if isPortError(got) || got == nil {
				t.Errorf("portError(%v) = %v, want a plain error", c.err, got)
			}
			continue
		}
		if !errors.Is(got, c.want) || got.Error() != c.err.Error() {
			t.Errorf("portError(%v) = %v, want %v keeping AWS's message", c.err, got, c.want)
		}
	}
}

func TestSessionName(t *testing.T) {
	if got := sessionName("webshop/aws prod"); got != "c4factory-webshop-aws-prod" {
		t.Errorf("sessionName = %q", got)
	}
	if got := sessionName(strings.Repeat("n", 100) + "/t"); len(got) != 64 {
		t.Errorf("sessionName is %d characters, STS takes 64", len(got))
	}
}

func params(ps []types.Parameter) map[string]string {
	out := map[string]string{}
	for _, p := range ps {
		out[aws.ToString(p.ParameterKey)] = aws.ToString(p.ParameterValue)
	}
	return out
}

// The target's credential is the only one used: no environment, shared
// config file or instance role leaks in.
func TestTargetConfigUsesOnlyTheSecret(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFROMENV")
	t.Setenv("AWS_REGION", "us-east-1")
	cfg, err := targetConfig(target("AKIAUSER", "production"))
	if err != nil {
		t.Fatal(err)
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil || creds.AccessKeyID != "AKIAUSER" || cfg.Region != testRegion {
		t.Errorf("config = %s in %s, %v; want AKIAUSER in %s", creds.AccessKeyID, cfg.Region, err, testRegion)
	}
	if c := AWSClients(cfg); c.CloudFormation == nil || c.S3 == nil || c.STS == nil || c.SSM == nil {
		t.Error("AWSClients left a client nil")
	}
	tt := target("AKIAUSER", "production")
	delete(tt.Settings, model.CloudFormationSettingRegion)
	if _, err := targetConfig(tt); err == nil || isPortError(err) {
		t.Errorf("targetConfig() without a region = %v, want a plain error", err)
	}
}

// editedFiles are framework's release files with its production manifest's
// artifacts changed by edit.
func editedFiles(t *testing.T, framework string, edit func(artifacts map[string]map[string]any)) []ports.File {
	t.Helper()
	files := fixtureFiles(t, framework)
	for i, f := range files {
		if f.Name != "production.manifest.json" {
			continue
		}
		body, _, err := f.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.NewDecoder(body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		arts := map[string]map[string]any{}
		for k, v := range m["artifacts"].(map[string]any) {
			arts[k] = v.(map[string]any)
		}
		edit(arts)
		m["artifacts"] = arts
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		files[i] = ports.File{Name: f.Name, Size: int64(len(b)),
			Open: func(context.Context) (io.ReadCloser, int64, error) {
				return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
			}}
	}
	return files
}

// A two-stack assembly isn't Deployed while only its first stack runs the
// version: State says Deploying until the second does too (#1608).
func TestStateMultiStack(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := editedFiles(t, "aws-lambda-ts", func(arts map[string]map[string]any) {
		second := maps.Clone(arts["price-fn"])
		props := maps.Clone(second["properties"].(map[string]any))
		props["stackName"] = "price-fn-api"
		second["properties"] = props
		second["dependencies"] = []any{"price-fn", "price-fn.assets"}
		arts["price-fn-api"] = second
	})
	ctx := context.Background()
	s := shipment("aws-lambda-ts", "v1.0.0", files)
	if err := w.d.Ship(ctx, tt, s); err != nil {
		t.Fatal(err)
	}
	st, err := w.d.State(ctx, tt, testApp, s.Version)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != ports.PhaseDeploying || a.stacks["price-fn-api"] != nil {
		t.Errorf("State() = %+v with the second stack not created, want Deploying", st)
	}
	shipUntilDeployed(t, w.d, tt, s)
	if n := a.appExecutions(testApp); n != 2 {
		t.Errorf("executions = %d, want one per stack", n)
	}
	for _, name := range []string{"price-fn", "price-fn-api"} {
		if got := a.stacks[name].tags[model.CloudFormationTagStacks]; got != "2" {
			t.Errorf("stack %s tag %s = %q, want 2", name, model.CloudFormationTagStacks, got)
		}
	}
}

// After an update rolls back, wanting the version it rolled back to is
// Deployed at once: its empty change set and State agree (#1608).
func TestStateRolledBackToWanted(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := fixtureFiles(t, "aws-lambda-ts")
	shipUntilDeployed(t, w.d, tt, shipment("aws-lambda-ts", "v1.0.0", files))
	a.fail["price-fn"] = true
	ctx := context.Background()
	if err := w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.1.0", files)); err != nil {
		t.Fatal(err)
	}
	if st, _ := w.d.State(ctx, tt, testApp, "v1.1.0"); st.Phase != ports.PhaseFailed {
		t.Fatalf("State(v1.1.0) = %+v, want Failed", st)
	}
	// CloudFormation finds the old version's change set empty.
	a.empty["price-fn"] = true
	if err := w.d.Ship(ctx, tt, shipment("aws-lambda-ts", "v1.0.0", files)); err != nil {
		t.Fatal(err)
	}
	st, err := w.d.State(ctx, tt, testApp, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "v1.0.0" || st.Phase != ports.PhaseDeployed || st.Health != ports.HealthHealthy {
		t.Errorf("State(v1.0.0) = %+v, want v1.0.0 Deployed", st)
	}
	if n := a.appExecutions(testApp); n != 2 {
		t.Errorf("executions = %d, want 2: nothing ships the running version again", n)
	}
}

// A stack whose environment names another region than the target's is
// refused, naming both, before anything is touched (#1608).
func TestShipOtherRegion(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	files := editedFiles(t, "aws-lambda-ts", func(arts map[string]map[string]any) {
		arts["price-fn"]["environment"] = "aws://unknown-account/us-east-1"
	})
	err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", files))
	if err == nil || isPortError(err) || !strings.Contains(err.Error(), "price-fn") ||
		!strings.Contains(err.Error(), "us-east-1") || !strings.Contains(err.Error(), testRegion) {
		t.Errorf("Ship() = %v, want a plain error naming the stack and both regions", err)
	}
	if len(a.changeSets) != 0 || len(a.puts) != 0 {
		t.Errorf("Ship touched the account: %d change sets, %d puts", len(a.changeSets), len(a.puts))
	}
}

// Ship waits a few seconds at most for a change set (#1608).
func TestShipChangeSetWaitIsShort(t *testing.T) {
	d := New(nil)
	if wait := time.Duration(d.polls) * d.delay; wait > 5*time.Second {
		t.Errorf("Ship waits up to %s for a change set, want at most 5s", wait)
	}
}

// A change set not ready when Ship stops waiting is executed by a later
// Ship, by its name, not made again (#1608).
func TestShipChangeSetContinues(t *testing.T) {
	w := newWorld(t)
	a, tt := w.bootstrapped()
	w.d.polls = 0
	s := shipment("aws-lambda-ts", "v1.0.0", fixtureFiles(t, "aws-lambda-ts"))
	if err := w.d.Ship(context.Background(), tt, s); err != nil {
		t.Fatal(err)
	}
	if len(a.appChangeSets()) != 1 || a.appExecutions(testApp) != 0 {
		t.Fatalf("change sets %d, executions %d; want 1 pending and none", len(a.appChangeSets()),
			a.appExecutions(testApp))
	}
	shipUntilDeployed(t, w.d, tt, s)
	if len(a.appChangeSets()) != 1 || a.appExecutions(testApp) != 1 {
		t.Errorf("change sets %d, executions %d; want the one change set executed", len(a.appChangeSets()),
			a.appExecutions(testApp))
	}
}

// A release file that fails to open or read is ErrUnreachable, retried, not
// a failed deploy (#1608).
func TestShipReleaseFileUnreadable(t *testing.T) {
	broken := errors.New("connection reset by peer")
	for name, open := range map[string]func(context.Context) (io.ReadCloser, int64, error){
		"open": func(context.Context) (io.ReadCloser, int64, error) { return nil, 0, broken },
		"read": func(context.Context) (io.ReadCloser, int64, error) {
			return io.NopCloser(iotest.ErrReader(broken)), 0, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			_, tt := w.bootstrapped()
			files := fixtureFiles(t, "aws-lambda-ts")
			for i := range files {
				if files[i].Name == "production.manifest.json" {
					files[i].Open = open
				}
			}
			err := w.d.Ship(context.Background(), tt, shipment("aws-lambda-ts", "v1.0.0", files))
			if !errors.Is(err, ports.ErrUnreachable) || !errors.Is(err, broken) {
				t.Errorf("Ship() = %v, want ErrUnreachable", err)
			}
		})
	}
}
