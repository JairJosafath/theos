package scaffold

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/c4factory/internal/model"
)

// templatesDir is the repository's templates/ folder, which the operator's
// catalog pins and hack/render reads.
const templatesDir = "../../templates"

type side struct{ Side, Name string }

// templateCombos renders every template in templates/ in at least one
// realistic combination of layers, the way a Container picks them.
var templateCombos = []struct {
	name   string
	typ    string
	layers []side
}{
	{"fastapi service with postgres", "service", []side{
		{model.SideFramework, "python-fastapi"},
		{model.SideStandard, "python-standard"},
		{model.SideCompute, "small"},
		{model.SideDeployment, "k8s-manifests"},
		{model.SideConnector, "postgres"},
	}},
	{"fastapi worker", "worker", []side{
		{model.SideFramework, "python-fastapi"},
		{model.SideDeployment, "k8s-manifests"},
	}},
	{"go cli", "cli", []side{
		{model.SideFramework, "go-cli"},
		{model.SideStandard, "go-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"python cli", "cli", []side{
		{model.SideFramework, "python-cli"},
		{model.SideStandard, "python-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"python worker on kubernetes", "worker", []side{
		{model.SideFramework, "python-cli"},
		{model.SideStandard, "python-standard"},
		{model.SideCompute, "small"},
		{model.SideDeployment, "k8s-manifests"},
		{model.SideConnector, "postgres"},
	}},
	{"vscode extension", "extension", []side{
		{model.SideFramework, "vscode-extension"},
		{model.SideStandard, "ts-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"kubebuilder operator", "operator", []side{
		{model.SideFramework, "go-kubebuilder"},
		{model.SideStandard, "go-operator-standard"},
		{model.SideCompute, "small"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"react web app", "static-site", []side{
		{model.SideFramework, "react-vite"},
		{model.SideStandard, "ts-standard"},
	}},
	{"react web app on kubernetes", "static-site", []side{
		{model.SideFramework, "react-vite"},
		{model.SideStandard, "ts-standard"},
		{model.SideCompute, "small"},
		{model.SideDeployment, "k8s-manifests"},
	}},
	{"node cli", "cli", []side{
		{model.SideFramework, "node-cli"},
		{model.SideStandard, "ts-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws lambda function", "function", []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws lambda function with dynamodb", "function", []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws ec2 service", "service", []side{
		{model.SideFramework, "aws-ec2-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws fargate service", "service", []side{
		{model.SideFramework, "aws-fargate-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws lambda function in go", "function", []side{
		{model.SideFramework, "aws-lambda-go"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws python lambda function", "function", []side{
		{model.SideFramework, "aws-lambda-py"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws queue consumer", "worker", []side{
		{model.SideFramework, "aws-queue-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws scheduled job", "job", []side{
		{model.SideFramework, "aws-schedule-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws event consumer", "worker", []side{
		{model.SideFramework, "aws-event-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws static site", "static-site", []side{
		{model.SideFramework, "aws-static-site-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"hello", "cli", []side{
		{model.SideFramework, "hello"},
		{model.SideStandard, "hello-standard"},
	}},
	{"hello html", "static-site", []side{
		{model.SideFramework, "hello-html"},
		{model.SideStandard, "hello-html-standard"},
	}},
}

func TestEveryTemplateRenders(t *testing.T) {
	for _, c := range templateCombos {
		t.Run(c.name, func(t *testing.T) {
			layers, result := renderCombo(t, c.typ, c.layers)
			for _, p := range result.Files.Paths() {
				if ext := path.Ext(p); ext != ".yaml" && ext != ".yml" {
					continue
				}
				if err := parseYAML(result.Files[p].Data); err != nil {
					t.Errorf("%s: %s doesn't parse as YAML: %v", ownerOf(layers, p), p, err)
				}
			}
		})
	}
}

// TestK8sManifestsFollowsTheFramework checks that k8s-manifests writes the
// Dockerfile, .dockerignore, port and probes of the Framework's image, no
// Dockerfile for a Framework it has none for, and no Service for a worker.
// python-fastapi's Deployment loads OIDC_ISSUER and OIDC_AUDIENCE from
// ConfigMap <container>-auth, which only python-fastapi gets (#1540).
func TestK8sManifestsFollowsTheFramework(t *testing.T) {
	cases := []struct {
		framework, typ string
		standard       string
		connector      string
		dockerfile     []string // substrings of the Dockerfile; nil: no Dockerfile
		dockerignore   []string // lines of .dockerignore
		deployment     []string // substrings of deploy/k8s/deployment.yaml
		never          []string // substrings neither the Dockerfile nor deployment.yaml has
	}{
		// #1540: python-fastapi refuses to start without OIDC_ISSUER and
		// OIDC_AUDIENCE; the Deployment loads both from sample-app-auth.
		{"python-fastapi", "service", "", "", []string{"ghcr.io/astral-sh/uv", "uvicorn", "EXPOSE 8000"},
			[]string{".venv", ".pytest_cache", ".ruff_cache", ".mypy_cache", ".uv", "dist"},
			[]string{"containerPort: 8000", "envFrom:\n            # The token issuer and audience the app verifies against.\n            - configMapRef:\n                name: sample-app-auth",
				"readinessProbe:\n            httpGet:\n              path: /healthz",
				"livenessProbe:\n            httpGet:\n              path: /healthz"},
			[]string{"PGPASSWORD", "/readyz"}},
		// #1528: with postgres, readiness asks /readyz (SELECT 1), so a pod
		// whose database is down leaves the Service; liveness stays on
		// /healthz, so the outage never restarts it.
		{"python-fastapi", "service", "", "postgres", []string{"uvicorn", "EXPOSE 8000"}, nil,
			[]string{"PGPASSWORD", "name: sample-app-auth\n            # Connection settings, one ConfigMap per Connector.\n            - configMapRef:\n                name: sample-app-postgres",
				"readinessProbe:\n            httpGet:\n              path: /readyz",
				"livenessProbe:\n            httpGet:\n              path: /healthz"}, nil},
		{"react-vite", "static-site", "ts-standard", "",
			[]string{"npm ci", "npm run build", "nginxinc/nginx-unprivileged", "/app/dist", "USER 101", "EXPOSE 8080"},
			[]string{"node_modules", "dist", "coverage"},
			[]string{"containerPort: 8080", "path: /\n"}, nil},
		// #1468: a python-cli worker runs python -m app, serves nothing and
		// has no probes.
		{"python-cli", "worker", "python-standard", "",
			[]string{"ghcr.io/astral-sh/uv", "uv sync --no-dev", "USER 1000:1000", `CMD ["python", "-m", "app"]`},
			[]string{".venv", ".pytest_cache", ".uv", "dist", "tests"},
			[]string{"image: sample-app:dev", "runAsNonRoot: true"},
			[]string{"EXPOSE", "uvicorn", "containerPort", "Probe", "-auth"}},
		{"go-cli", "service", "", "", nil, nil, []string{"containerPort: 8000"}, []string{"envFrom", "-auth"}},
	}
	for _, c := range cases {
		t.Run(strings.TrimSuffix(c.framework+"+"+c.connector, "+"), func(t *testing.T) {
			sides := []side{{model.SideFramework, c.framework}}
			if c.standard != "" {
				sides = append(sides, side{model.SideStandard, c.standard})
			}
			sides = append(sides, side{model.SideDeployment, "k8s-manifests"})
			if c.connector != "" {
				sides = append(sides, side{model.SideConnector, c.connector})
			}
			_, result := renderCombo(t, c.typ, sides)
			for _, name := range []string{"Dockerfile", ".dockerignore"} {
				if _, ok := result.Files[name]; ok != (c.dockerfile != nil) {
					t.Errorf("%s written = %v, want %v", name, ok, c.dockerfile != nil)
				}
			}
			if c.dockerfile != nil {
				assertContains(t, "Dockerfile", string(result.Files["Dockerfile"].Data), c.dockerfile)
				lines := strings.Split(string(result.Files[".dockerignore"].Data), "\n")
				for _, w := range c.dockerignore {
					if !slices.Contains(lines, w) {
						t.Errorf(".dockerignore doesn't list %q:\n%s", w, result.Files[".dockerignore"].Data)
					}
				}
			}
			if _, ok := result.Files["deploy/k8s/service.yaml"]; ok != (c.typ != "worker") {
				t.Errorf("deploy/k8s/service.yaml written = %v for a %s, want it for every type but worker", ok, c.typ)
			}
			auth, ok := result.Files["deploy/k8s/auth.yaml"]
			if fastapi := c.framework == "python-fastapi"; ok != fastapi {
				t.Errorf("deploy/k8s/auth.yaml written = %v, want %v", ok, fastapi)
			} else if fastapi {
				assertContains(t, "deploy/k8s/auth.yaml", string(auth.Data),
					[]string{"kind: ConfigMap", "name: sample-app-auth", "OIDC_ISSUER: https://", "OIDC_AUDIENCE: sample-app"})
				assertContains(t, "deploy/k8s/kustomization.yaml", string(result.Files["deploy/k8s/kustomization.yaml"].Data),
					[]string{"  - auth.yaml"})
			}
			deployment := string(result.Files["deploy/k8s/deployment.yaml"].Data)
			assertContains(t, "deploy/k8s/deployment.yaml", deployment, c.deployment)
			for _, w := range c.never {
				if strings.Contains(string(result.Files["Dockerfile"].Data), w) || strings.Contains(deployment, w) {
					t.Errorf("the Dockerfile or deploy/k8s/deployment.yaml contains %q", w)
				}
			}
		})
	}
}

// TestFastAPIConnectsOnlyWithPostgres checks that python-fastapi writes
// app/db.py, a /readyz that runs SELECT 1, its tests and psycopg's pool only
// when the Container has the postgres Connector (#1472).
func TestFastAPIConnectsOnlyWithPostgres(t *testing.T) {
	files := map[string][]string{
		"app/db.py":          {"ConnectionPool(", `"SELECT 1"`, "PGPASSWORD"},
		"app/main.py":        {"db.open_pool()", `@app.get("/readyz")`, "db.ping(pool)", "status_code = 503"},
		"tests/test_main.py": {"def test_readyz_when_the_database_answers", "def test_readyz_when_the_database_fails", "DATABASE_URL reaches no server"},
		"pyproject.toml":     {`"psycopg[binary,pool]>=3.3"`},
		"uv.lock":            {`name = "psycopg-pool"`, `extras = ["binary", "pool"]`},
	}
	for _, postgres := range []bool{false, true} {
		sides := []side{{model.SideFramework, "python-fastapi"}, {model.SideStandard, "python-standard"}}
		if postgres {
			sides = append(sides, side{model.SideConnector, "postgres"})
		}
		_, result := renderCombo(t, "service", sides)
		if _, ok := result.Files["app/db.py"]; ok != postgres {
			t.Errorf("postgres %v: app/db.py written = %v", postgres, ok)
		}
		for name, want := range files {
			got := string(result.Files[name].Data)
			if postgres {
				assertContains(t, name, got, want)
				continue
			}
			for _, w := range want {
				if strings.Contains(got, w) {
					t.Errorf("without postgres, %s contains %q", name, w)
				}
			}
		}
	}
}

// TestFastAPILogsJSONLines checks that python-fastapi, with or without the
// postgres Connector, writes app/logs.py, installs its request logger and
// carries the tests that show one JSON line per request (#1473).
func TestFastAPILogsJSONLines(t *testing.T) {
	files := map[string][]string{
		"app/logs.py":        {"class JSONFormatter", `"LOG_LEVEL"`, `REQUEST_ID_HEADER = "X-Request-Id"`, "access.disabled = True", "request.url.path"},
		"app/main.py":        {"logs.configure()", `app.middleware("http")(logs.log_request)`},
		"tests/test_main.py": {"def test_one_json_line_per_request", "def test_log_level_comes_from_the_environment", "def test_uvicorns_access_log_is_off"},
	}
	for _, postgres := range []bool{false, true} {
		sides := []side{{model.SideFramework, "python-fastapi"}, {model.SideStandard, "python-standard"}}
		if postgres {
			sides = append(sides, side{model.SideConnector, "postgres"})
		}
		_, result := renderCombo(t, "service", sides)
		for name, want := range files {
			assertContains(t, name, string(result.Files[name].Data), want)
		}
	}
}

// TestFastAPIVerifiesABearerJWT checks that python-fastapi, with and without
// postgres, writes app/auth.py, puts every route but the probes behind it,
// refuses to start without an issuer and tests the token cases (#1474).
func TestFastAPIVerifiesABearerJWT(t *testing.T) {
	files := map[string][]string{
		"app/auth.py":        {`ALGORITHMS = ["RS256", "ES256"]`, `"WWW-Authenticate": "Bearer"`, "OIDC_ISSUER", "OIDC_AUDIENCE", "AUTH_DISABLED"},
		"app/main.py":        {"api = APIRouter(dependencies=[Depends(auth.verified_claims)])", "app.state.verifier = auth.from_env()", `@app.get("/healthz")`, "app.include_router(api)"},
		"pyproject.toml":     {`"pyjwt[crypto]>=2.15"`},
		"uv.lock":            {`name = "pyjwt"`, `name = "cryptography"`},
		"README.md":          {"## Auth", "OIDC_ISSUER", "OIDC_AUDIENCE", "AUTH_DISABLED=true"},
		"tests/test_main.py": {"HS256", "expired", "audience"},
	}
	for _, postgres := range []bool{false, true} {
		sides := []side{{model.SideFramework, "python-fastapi"}, {model.SideStandard, "python-standard"}}
		if postgres {
			sides = append(sides, side{model.SideConnector, "postgres"})
		}
		_, result := renderCombo(t, "service", sides)
		for name, want := range files {
			assertContains(t, name, string(result.Files[name].Data), want)
		}
	}
}

// TestK8sManifestsBuildsItsImageOnRelease checks that k8s-manifests writes,
// for Gitea Actions, the release run that builds the image on the image
// runner for both platforms with an SBOM and provenance, into the archive the
// operator pushes (#1457).
func TestK8sManifestsBuildsItsImageOnRelease(t *testing.T) {
	_, result := renderCombo(t, "service", []side{
		{model.SideFramework, "python-fastapi"},
		{model.SideDeployment, "k8s-manifests"},
	})
	f, ok := result.Files[".gitea/workflows/image.yaml"]
	if !ok {
		t.Fatalf(".gitea/workflows/image.yaml not written; files: %v", result.Files.Paths())
	}
	assertContains(t, "image.yaml", string(f.Data), []string{
		`tags: ["v*"]`, "runs-on: image", "buildctl-daemonless.sh build",
		"platform=linux/amd64,linux/arm64",
		"attest:sbom=generator=docker/buildkit-syft-scanner:1.9.0@sha256:",
		"attest:provenance=mode=min", `type=oci,dest=$archive`,
		`${REPOSITORY##*/}-$TAG.oci.tar`, "No Dockerfile at the root", `$API/releases`,
	})
}

// TestK8sManifestsWritesItsREADME checks that k8s-manifests writes
// deploy/k8s/README.md (#1470): how to build the image, set it, preview and
// apply the manifests, for the Container, with the files the other layers
// wrote and the Framework's port and probes.
func TestK8sManifestsWritesItsREADME(t *testing.T) {
	cases := map[string]struct {
		want  []string
		never []string
	}{
		"fastapi service with postgres": {
			want: []string{
				"# Deploying sample-app to Kubernetes",
				"docker build -t sample-app:1.0.0 .",
				"kustomize edit set image sample-app=registry.example.com/sample-app:1.0.0",
				"kustomize build deploy/k8s",
				"kubectl apply -k deploy/k8s",
				"`service.yaml`: Service `sample-app` on port 80",
				"`compute.yaml`: the CPU and memory requests and limits",
				"(small)",
				"`connector-postgres.yaml`: the database",
				// #1471: the user makes the password's Secret before applying.
				"kubectl create secret generic sample-app-postgres --from-literal=password=",
				"port 8000",
				// #1528: readiness on /readyz, liveness on /healthz, and why.
				"The readiness probe\nasks `GET /readyz`",
				"the pod leaves Service `sample-app`",
				"`GET /healthz`",
				"a database\noutage never restarts the app",
				// #1540: the user sets the issuer and audience before applying.
				"ConfigMap\n`sample-app-auth` in `auth.yaml`",
				"`auth.yaml`: ConfigMap `sample-app-auth` with `OIDC_ISSUER`",
			},
			never: []string{"brings its own `Dockerfile`", "no port and no probes"},
		},
		"react web app on kubernetes": {
			want: []string{
				"# Deploying sample-app to Kubernetes",
				"docker build -t sample-app:1.0.0 .",
				"kubectl apply -k deploy/k8s",
				"`service.yaml`: Service `sample-app`",
				"`compute.yaml`",
				"port 8080",
				"`GET /` on it",
			},
			never: []string{"connector-", "/healthz", "OIDC_ISSUER"},
		},
		"python worker on kubernetes": {
			want:  []string{"`connector-postgres.yaml`", "no port and no probes"},
			never: []string{"service.yaml", "GET "},
		},
	}
	for _, c := range templateCombos {
		want, ok := cases[c.name]
		if !ok {
			continue
		}
		delete(cases, c.name)
		t.Run(c.name, func(t *testing.T) {
			_, result := renderCombo(t, c.typ, c.layers)
			readme, ok := result.Files["deploy/k8s/README.md"]
			if !ok {
				t.Fatal("deploy/k8s/README.md not written")
			}
			assertContains(t, "deploy/k8s/README.md", string(readme.Data), want.want)
			for _, w := range want.never {
				if strings.Contains(string(readme.Data), w) {
					t.Errorf("deploy/k8s/README.md contains %q:\n%s", w, readme.Data)
				}
			}
		})
	}
	for name := range cases {
		t.Errorf("no combo %q in templateCombos", name)
	}
}

// TestGiteaReleaseWritesReleasingGuide checks that gitea-release writes
// RELEASING.md (#1475): how to tag a release, what the workflow attaches,
// how to check it with make dist and what to do when a run fails, with the
// version file the Standard's make dist checks the tag against.
func TestGiteaReleaseWritesReleasingGuide(t *testing.T) {
	common := []string{
		"# Releasing sample-app",
		"git tag v0.2.0",
		"git push origin v0.2.0",
		"`make dist VERSION=<tag>`",
		"every file directly in `dist/`",
		"## Check a release locally",
		"ls dist/",
		"## When a run fails",
		"git tag -f v0.2.0 && git push -f origin v0.2.0",
		"delete the asset on the release page",
	}
	cases := map[string]struct {
		want  []string
		never []string
	}{
		"python cli": {
			want: []string{"the wheel and the sdist", "Set the version in `pyproject.toml`",
				"make dist VERSION=v0.2.0   # the version in pyproject.toml"},
			never: []string{"package.json", "make dist VERSION=v0.0.0-test\n"},
		},
		"node cli": {
			want: []string{"the npm tarball (`.tgz`)", "Set the version in `package.json`",
				"make dist VERSION=v0.2.0   # the version in package.json"},
			never: []string{"pyproject.toml", "make dist VERSION=v0.0.0-test\n"},
		},
		"go cli": {
			want:  []string{"`SHA256SUMS`", "make dist VERSION=v0.0.0-test\n"},
			never: []string{"pyproject.toml", "package.json"},
		},
	}
	for _, c := range templateCombos {
		want, ok := cases[c.name]
		if !ok {
			continue
		}
		delete(cases, c.name)
		t.Run(c.name, func(t *testing.T) {
			_, result := renderCombo(t, c.typ, c.layers)
			f, ok := result.Files["RELEASING.md"]
			if !ok {
				t.Fatalf("RELEASING.md not written; files: %v", result.Files.Paths())
			}
			assertContains(t, "RELEASING.md", string(f.Data), slices.Concat(common, want.want))
			for _, w := range want.never {
				if strings.Contains(string(f.Data), w) {
					t.Errorf("RELEASING.md contains %q:\n%s", w, f.Data)
				}
			}
		})
	}
	for name := range cases {
		t.Errorf("no combo %q in templateCombos", name)
	}
}

// TestGoStandardCIKeepsFactoryModulesOffPublicProxies checks go-standard's CI:
// the factory's c4factory.local modules come from the owner's package registry
// alone, before make check, and make check never asks a proxy for them. A
// GOPROXY list of the registry, then the public proxy, would ask the public one
// for a module the registry lacks: the go command goes on after a 404.
func TestGoStandardCIKeepsFactoryModulesOffPublicProxies(t *testing.T) {
	_, result := renderCombo(t, "cli", []side{
		{model.SideFramework, "go-cli"},
		{model.SideStandard, "go-standard"},
		{model.SideDeployment, "gitea-release"},
	})
	const registry = "${{ gitea.server_url }}/api/packages/${{ gitea.repository_owner }}/go"
	var workflow struct {
		Jobs map[string]struct {
			Env   map[string]string `yaml:"env"`
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(result.Files[".gitea/workflows/check.yaml"].Data, &workflow); err != nil {
		t.Fatal(err)
	}
	job := workflow.Jobs["check"]
	if job.Env["GOPRIVATE"] != "c4factory.local" || strings.Contains(job.Env["GOPROXY"], "/api/packages/") {
		t.Errorf("job env %v: want GOPRIVATE=c4factory.local and the public proxy only", job.Env)
	}
	fetched, checked := -1, -1
	for i, step := range job.Steps {
		if step.Env["GOPROXY"] != "" {
			// GOPRIVATE set and empty: the job's would send the paths direct.
			private, set := step.Env["GOPRIVATE"]
			if step.Env["GOPROXY"] != registry || !set || private != "" ||
				step.Env["GONOSUMDB"] != "c4factory.local" {
				t.Errorf("step %q env %v: want GOPROXY=%s alone, an empty GOPRIVATE, GONOSUMDB=c4factory.local",
					step.Name, step.Env, registry)
			}
			if !strings.Contains(step.Run, "go.sum") || !strings.Contains(step.Run, "go mod download") {
				t.Errorf("step %q doesn't download the factory's modules:\n%s", step.Name, step.Run)
			}
			fetched = i
		}
		if strings.TrimSpace(step.Run) == "make check" {
			checked = i
		}
	}
	if fetched < 0 || checked < fetched {
		t.Errorf("the registry step is #%d and make check #%d; want the registry first", fetched, checked)
	}
}

func assertContains(t *testing.T, name, got string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s doesn't contain %q:\n%s", name, w, got)
		}
	}
}

// renderCombo builds a Container of type typ from the templates in
// templates/, with the choices the layers make.
func renderCombo(t *testing.T, typ string, sides []side) ([]Layer, Result) {
	t.Helper()
	return renderComboChecks(t, typ, sides, "")
}

// renderComboChecks is renderCombo with the Container's effective checks.
func renderComboChecks(t *testing.T, typ string, sides []side, checks string) ([]Layer, Result) {
	t.Helper()
	in, layers := loadCombo(t, typ, sides, checks)

	// Build names the template and file of a render error, and the
	// path and both templates of a conflict.
	result, err := Build(in, layers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return layers, result
}

// loadCombo reads the templates of sides from templates/ and returns them
// with the input of a Container of type typ on Gitea Actions.
func loadCombo(t *testing.T, typ string, sides []side, checks string) (Input, []Layer) {
	t.Helper()
	in := Input{
		Project:        "webshop",
		SoftwareSystem: "shop",
		Container:      "sample-app",
		DisplayName:    "Sample App",
		Type:           typ,
		Checks:         checks,
		// The operator always renders the resolved block: the defaults when
		// the Container has none.
		Operations: model.ResolveOperations(model.OperationsChoices{}),
		CI:         giteaActions(t),
	}
	layers := make([]Layer, 0, len(sides))
	for _, l := range sides {
		files, err := ReadTree(os.DirFS(filepath.Join(templatesDir, l.Name)))
		if err != nil {
			t.Fatalf("read template %s: %v", l.Name, err)
		}
		layers = append(layers, Layer{Side: l.Side, Name: l.Name, Ref: "local", Files: files})
		switch l.Side {
		case model.SideFramework:
			in.Framework = l.Name
		case model.SideStandard:
			in.Standard = l.Name
		case model.SideCompute:
			in.Compute = l.Name
		case model.SideDeployment:
			in.Deployment = l.Name
		case model.SideConnector:
			in.Connectors = append(in.Connectors, l.Name)
		}
	}
	return in, layers
}

// TestEveryTemplateHasACombo fails when a folder in templates/ is rendered by
// no row of templateCombos, so a new template can't go untested.
func TestEveryTemplateHasACombo(t *testing.T) {
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, c := range templateCombos {
		for _, l := range c.layers {
			covered[l.Name] = true
		}
	}
	for _, e := range entries {
		if e.IsDir() && !covered[e.Name()] {
			t.Errorf("templates/%s is rendered by no row of templateCombos; add a realistic combination for it", e.Name())
		}
	}
}

// parseYAML decodes every document in data.
func parseYAML(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// ownerOf names the layer that writes p, or the operator.
func ownerOf(layers []Layer, p string) string {
	for _, l := range layers {
		for src := range l.Files {
			if strings.TrimSuffix(src, templateSuffix) == p {
				return l.Name
			}
		}
	}
	return operatorOwner
}

// TestVSCodeExtensionGuidesAskForNoTestThatStartsVSCode checks ts-standard's
// guides for a vscode-extension: the sandbox and CI can't start VS Code, so no
// test does (#602).
func TestVSCodeExtensionGuidesAskForNoTestThatStartsVSCode(t *testing.T) {
	_, result := renderCombo(t, "extension", []side{
		{model.SideFramework, "vscode-extension"},
		{model.SideStandard, "ts-standard"},
	})
	for name, want := range map[string][]string{
		"AGENTS.md": {"No test starts VS Code",
			"`@vscode/test-electron` can't run in `make check`"},
		"docs/agents/implementer.md": {"don't write a test that starts it"},
		"docs/style/vscode.md":       {"No integration tests in a real VS Code", "can't run in `make check`"},
	} {
		file, ok := result.Files[name]
		if !ok {
			t.Errorf("%s not written", name)
			continue
		}
		assertContains(t, name, strings.Join(strings.Fields(string(file.Data)), " "), want)
	}
}
