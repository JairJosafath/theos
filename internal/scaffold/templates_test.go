package scaffold

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
		{model.SideDomain, "webshop-domain"},
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
	{"python job on kubernetes", "job", []side{
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
	{"the factory itself", "operator", []side{
		{model.SideFramework, "go-kubebuilder"},
		{model.SideStandard, "factory-self"},
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
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws lambda function with dynamodb", "function", []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws lambda function with rds", "function", []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "rds"},
	}},
	{"aws lambda function with dynamodb and rds", "function", []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
		{model.SideConnector, "rds"},
	}},
	{"aws ec2 service", "service", []side{
		{model.SideFramework, "aws-ec2-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws fargate service", "service", []side{
		{model.SideFramework, "aws-fargate-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws ec2 service with dynamodb", "service", []side{
		{model.SideFramework, "aws-ec2-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws fargate service with dynamodb", "service", []side{
		{model.SideFramework, "aws-fargate-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws lambda function in go", "function", []side{
		{model.SideFramework, "aws-lambda-go"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws lambda function in go with dynamodb", "function", []side{
		{model.SideFramework, "aws-lambda-go"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws lambda function in go with rds", "function", []side{
		{model.SideFramework, "aws-lambda-go"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "rds"},
	}},
	{"aws lambda function in go with dynamodb and rds", "function", []side{
		{model.SideFramework, "aws-lambda-go"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
		{model.SideConnector, "rds"},
	}},
	{"aws python lambda function", "function", []side{
		{model.SideFramework, "aws-lambda-py"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws python lambda function with dynamodb", "function", []side{
		{model.SideFramework, "aws-lambda-py"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws python lambda function with rds", "function", []side{
		{model.SideFramework, "aws-lambda-py"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "rds"},
	}},
	{"aws python lambda function with dynamodb and rds", "function", []side{
		{model.SideFramework, "aws-lambda-py"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
		{model.SideConnector, "rds"},
	}},
	{"aws queue consumer", "worker", []side{
		{model.SideFramework, "aws-queue-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws queue consumer with dynamodb", "worker", []side{
		{model.SideFramework, "aws-queue-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws queue consumer with rds", "worker", []side{
		{model.SideFramework, "aws-queue-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "rds"},
	}},
	{"aws queue consumer with dynamodb and rds", "worker", []side{
		{model.SideFramework, "aws-queue-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
		{model.SideConnector, "rds"},
	}},
	{"aws scheduled job", "job", []side{
		{model.SideFramework, "aws-schedule-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws scheduled job with dynamodb", "job", []side{
		{model.SideFramework, "aws-schedule-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws scheduled job with rds", "job", []side{
		{model.SideFramework, "aws-schedule-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "rds"},
	}},
	{"aws scheduled job with dynamodb and rds", "job", []side{
		{model.SideFramework, "aws-schedule-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
		{model.SideConnector, "rds"},
	}},
	{"aws event consumer", "worker", []side{
		{model.SideFramework, "aws-event-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws event consumer with dynamodb", "worker", []side{
		{model.SideFramework, "aws-event-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
	}},
	{"aws event consumer with rds", "worker", []side{
		{model.SideFramework, "aws-event-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "rds"},
	}},
	{"aws event consumer with dynamodb and rds", "worker", []side{
		{model.SideFramework, "aws-event-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
		{model.SideConnector, "dynamodb"},
		{model.SideConnector, "rds"},
	}},
	{"aws static site", "static-site", []side{
		{model.SideFramework, "aws-static-site-ts"},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
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
// TestK8sManifestsRunsAJob checks that for type job k8s-manifests writes
// job.yaml instead of deployment.yaml (#1587): a batch/v1 Job that runs once
// (restartPolicy Never), annotated for Argo CD to replace it at each release,
// with no TTL (the argoCD target's selfHeal would rerun a deleted Job), no
// port and no probes, loading each Connector's ConfigMap and PGPASSWORD like
// the Deployment; the kustomization lists it, and small's compute.yaml
// patches the Job.
func TestK8sManifestsRunsAJob(t *testing.T) {
	var combo []side
	for _, c := range templateCombos {
		if c.name == "python job on kubernetes" {
			combo = c.layers
		}
	}
	if combo == nil {
		t.Fatal(`no combo "python job on kubernetes" in templateCombos`)
	}
	_, result := renderCombo(t, "job", combo)
	for _, name := range []string{"deploy/k8s/deployment.yaml", "deploy/k8s/service.yaml"} {
		if _, ok := result.Files[name]; ok {
			t.Errorf("%s written for a job", name)
		}
	}
	job, ok := result.Files["deploy/k8s/job.yaml"]
	if !ok {
		t.Fatal("deploy/k8s/job.yaml not written for a job")
	}
	assertContains(t, "deploy/k8s/job.yaml", string(job.Data), []string{
		"apiVersion: batch/v1\nkind: Job\n", "name: sample-app\n",
		// #1628: Replace=true alone is an update, which the immutable template refuses; Force=true recreates.
		"argocd.argoproj.io/sync-options: Force=true,Replace=true",
		"backoffLimit: 3", "restartPolicy: Never", "image: sample-app:dev", "runAsNonRoot: true",
		"envFrom:\n            # Connection settings, one ConfigMap per Connector.\n            - configMapRef:\n                name: sample-app-postgres",
		"- name: PGPASSWORD\n              valueFrom:\n                secretKeyRef:\n                  name: sample-app-postgres\n                  key: password",
	})
	for _, w := range []string{"ttlSecondsAfterFinished", "containerPort", "Probe", "kind: Deployment"} {
		if strings.Contains(string(job.Data), w) {
			t.Errorf("deploy/k8s/job.yaml contains %q:\n%s", w, job.Data)
		}
	}
	kustomization := string(result.Files["deploy/k8s/kustomization.yaml"].Data)
	assertContains(t, "deploy/k8s/kustomization.yaml", kustomization,
		[]string{"resources:\n  - job.yaml\n  - connector-postgres.yaml\n", "patches:", "- path: compute.yaml", "`argoCD` DeployTarget"})
	if strings.Contains(kustomization, "deployment.yaml") {
		t.Errorf("deploy/k8s/kustomization.yaml lists deployment.yaml for a job:\n%s", kustomization)
	}
	compute, ok := result.Files["deploy/k8s/compute.yaml"]
	if !ok {
		t.Fatal("deploy/k8s/compute.yaml not written")
	}
	assertContains(t, "deploy/k8s/compute.yaml", string(compute.Data),
		[]string{"apiVersion: batch/v1\nkind: Job\n", "name: sample-app\n", "cpu: 50m"})
	if strings.Contains(string(compute.Data), "kind: Deployment") {
		t.Errorf("deploy/k8s/compute.yaml patches a Deployment for a job:\n%s", compute.Data)
	}
}

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

// TestGiteaReleaseBuildsAnOperatorsImage checks that gitea-release's release
// run, for Gitea Actions, builds an operator's image on the image runner
// before the release job, which names it by digest on the Git host's
// registry in make dist's IMG, and that a CLI's release has no image job
// (#1458).
func TestGiteaReleaseBuildsAnOperatorsImage(t *testing.T) {
	const workflow = ".gitea/workflows/release.yaml"
	_, result := renderCombo(t, "operator", []side{
		{model.SideFramework, "go-kubebuilder"},
		{model.SideStandard, "go-operator-standard"},
		{model.SideDeployment, "gitea-release"},
	})
	f, ok := result.Files[workflow]
	if !ok {
		t.Fatalf("%s not written for an operator; files: %v", workflow, result.Files.Paths())
	}
	assertContains(t, "operator "+workflow, string(f.Data), []string{
		"runs-on: image", "needs: image", "digest: ${{ steps.build.outputs.digest }}",
		"DIGEST: ${{ needs.image.outputs.digest }}", "buildctl-daemonless.sh build",
		"platform=linux/amd64,linux/arm64", `${REPOSITORY##*/}-$TAG.oci.tar`,
		"a release that builds an image needs one", `host=${SERVER_URL#*://}`,
		`repository=$(printf '%s' "$REPOSITORY" | tr A-Z a-z)`,
		`image="${host%%/*}/$repository@$DIGEST"`, `make dist VERSION="$TAG" IMG="$image"`,
		// #1581: the registry is at the host's root, whatever path ROOT_URL
		// has, and a digest that is not sha256: plus 64 hex fails the step.
		`grep -Eq '^sha256:[0-9a-f]{64}$' ||`,
		// #1583: the operator pushes a tag once, so a rerun asks the registry
		// for the tag's digest first (HEAD on the manifest, the job's token)
		// and names it when it holds one; 404 names the digest just built,
		// anything else fails the step.
		`curl -sS -I -o /dev/null -D "$headers" -w '%{http_code}' -u "x:$TOKEN"`,
		`"${SERVER_URL%%://*}://${host%%/*}/v2/$repository/manifests/$TAG"`,
		`held=$(tr -d '\r' < "$headers" | awk 'tolower($1) == "docker-content-digest:" { print $2 }')`,
		"DIGEST=$held ;;", "404) echo \"The registry does not hold $TAG yet",
		"*) echo \"Asking the registry for $TAG failed with HTTP $code", "exit 1 ;;",
	})
	// #1583: the lookup comes after the digest guard and before make dist,
	// so make dist runs with the digest the registry holds.
	for _, pair := range [][2]string{
		{`grep -Eq '^sha256:[0-9a-f]{64}$' ||`, `/v2/$repository/manifests/$TAG`},
		{`/v2/$repository/manifests/$TAG`, `DIGEST=$held ;;`},
		{`DIGEST=$held ;;`, `make dist VERSION="$TAG" IMG="$image"`},
	} {
		if strings.Index(string(f.Data), pair[0]) > strings.Index(string(f.Data), pair[1]) {
			t.Errorf("operator %s runs %q after %q (#1583)", workflow, pair[0], pair[1])
		}
	}
	if strings.Contains(string(f.Data), `image="${host%/}/`) {
		t.Errorf("operator %s keeps ROOT_URL's path in the image host (#1581)", workflow)
	}
	// Gitea's ${{ }} expressions stay; a Go template action would not parse.
	if strings.Contains(strings.ReplaceAll(string(f.Data), "${{", ""), "{{") {
		t.Errorf("operator %s keeps a template action:\n%s", workflow, f.Data)
	}

	// #1610: a Fargate service's release builds its image too, before the
	// release job, and its make dist names none: the stack takes it as a
	// deploy parameter.
	_, result = renderCombo(t, "service", []side{
		{model.SideFramework, model.FrameworkAWSFargate},
		{model.SideStandard, "aws-standard"},
		{model.SideDeployment, "gitea-release"},
	})
	f, ok = result.Files[workflow]
	if !ok {
		t.Fatalf("%s not written for a Fargate service; files: %v", workflow, result.Files.Paths())
	}
	assertContains(t, "fargate "+workflow, string(f.Data), []string{
		"runs-on: image", "needs: image", "buildctl-daemonless.sh build", `${REPOSITORY##*/}-$TAG.oci.tar`,
		`make dist VERSION="$TAG"`,
	})
	for _, never := range []string{"IMG=", "DIGEST:", "/v2/"} {
		if strings.Contains(string(f.Data), never) {
			t.Errorf("fargate %s contains %q, an operator's make dist:\n%s", workflow, never, f.Data)
		}
	}

	_, result = renderCombo(t, "cli", []side{
		{model.SideFramework, "go-cli"},
		{model.SideStandard, "go-standard"},
		{model.SideDeployment, "gitea-release"},
	})
	f, ok = result.Files[workflow]
	if !ok {
		t.Fatalf("%s not written for a CLI; files: %v", workflow, result.Files.Paths())
	}
	assertContains(t, "cli "+workflow, string(f.Data), []string{`make dist VERSION="$TAG"`})
	for _, never := range []string{"runs-on: image", "needs:", "IMG=", "DIGEST", "buildctl", "/v2/"} {
		if strings.Contains(string(f.Data), never) {
			t.Errorf("cli %s contains %q, an operator's image job:\n%s", workflow, never, f.Data)
		}
	}
}

// TestGiteaReleaseNamesTheImageAsTheOperatorPushesIt runs the make dist step
// of an operator's release, extracted from the rendered workflow, against a
// stub make that records the IMG it receives (#1581): a sub-path in the Git
// host's ROOT_URL stays out of the image host, the repository is lowercased,
// and a digest that is not sha256: plus 64 hex fails the step before make.
func TestGiteaReleaseNamesTheImageAsTheOperatorPushesIt(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	_, result := renderCombo(t, "operator", []side{
		{model.SideFramework, "go-kubebuilder"},
		{model.SideStandard, "go-operator-standard"},
		{model.SideDeployment, "gitea-release"},
	})
	var w workflow
	if err := yaml.Unmarshal(result.Files[".gitea/workflows/release.yaml"].Data, &w); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range w.Jobs["release"].Steps {
		if step.Name == "make dist" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatalf("no make dist step in the release job: %+v", w.Jobs["release"].Steps)
	}
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const held = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	cases := []struct {
		name, serverURL, digest string
		// code is the registry's answer to HEAD on the tag's manifest, 404
		// by default; held the Docker-Content-Digest header it sends.
		code, held string
		wantIMG    string // "" when the step must fail before make dist
		wantSays   string // in the step's output when it fails
		wantAsks   string // the manifest URL the step asks the registry; "" for never
	}{
		{name: "a host alone", serverURL: "https://git.example.com", digest: digest,
			wantIMG:  "git.example.com/owner/repo@" + digest,
			wantAsks: "https://git.example.com/v2/owner/repo/manifests/v1.2.3"},
		{name: "a host with a port", serverURL: "http://gitea.local:3000", digest: digest,
			wantIMG:  "gitea.local:3000/owner/repo@" + digest,
			wantAsks: "http://gitea.local:3000/v2/owner/repo/manifests/v1.2.3"},
		{name: "a ROOT_URL with a sub-path", serverURL: "https://git.example.com/gitea", digest: digest,
			wantIMG:  "git.example.com/owner/repo@" + digest,
			wantAsks: "https://git.example.com/v2/owner/repo/manifests/v1.2.3"},
		{name: "a ROOT_URL with a trailing slash", serverURL: "https://git.example.com/", digest: digest,
			wantIMG:  "git.example.com/owner/repo@" + digest,
			wantAsks: "https://git.example.com/v2/owner/repo/manifests/v1.2.3"},
		{name: "an empty digest", serverURL: "https://git.example.com", wantSays: "gave no digest"},
		{name: "a null digest", serverURL: "https://git.example.com", digest: "null", wantSays: "gave no digest"},
		{name: "a digest without its algorithm", serverURL: "https://git.example.com",
			digest: digest[len("sha256:"):], wantSays: "gave no digest"},
		{name: "a short digest", serverURL: "https://git.example.com", digest: "sha256:0123456789abcdef",
			wantSays: "gave no digest"},
		{name: "a digest that is not hex", serverURL: "https://git.example.com",
			digest: "sha256:" + strings.Repeat("xy", 32), wantSays: "gave no digest"},
		// #1583: the operator pushes a tag once, so the release files name
		// the digest the registry holds for the tag when it holds one, and
		// the one just built only on a first run.
		{name: "a tag the registry holds", serverURL: "https://git.example.com", digest: digest,
			code: "200", held: held, wantIMG: "git.example.com/owner/repo@" + held,
			wantAsks: "https://git.example.com/v2/owner/repo/manifests/v1.2.3"},
		{name: "a tag the registry holds as the digest just built", serverURL: "https://git.example.com",
			digest: digest, code: "200", held: digest, wantIMG: "git.example.com/owner/repo@" + digest,
			wantAsks: "https://git.example.com/v2/owner/repo/manifests/v1.2.3"},
		{name: "a tag the registry holds without a digest", serverURL: "https://git.example.com",
			digest: digest, code: "200", wantSays: "gave no digest for it",
			wantAsks: "https://git.example.com/v2/owner/repo/manifests/v1.2.3"},
		{name: "a registry that fails", serverURL: "https://git.example.com", digest: digest,
			code: "500", wantSays: "HTTP 500",
			wantAsks: "https://git.example.com/v2/owner/repo/manifests/v1.2.3"},
		{name: "a registry that refuses the token", serverURL: "https://git.example.com", digest: digest,
			code: "401", wantSays: "HTTP 401",
			wantAsks: "https://git.example.com/v2/owner/repo/manifests/v1.2.3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			got := filepath.Join(dir, "make.args")
			asked := filepath.Join(dir, "curl.url")
			write(t, bin, "make", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+got+"\"\n")
			// The registry: records the URL asked (curl's last argument),
			// writes the headers to -D's file and prints the status code.
			code := c.code
			if code == "" {
				code = "404"
			}
			write(t, bin, "curl", "#!/bin/sh\n"+
				"for a; do url=$a; done\nprintf '%s\\n' \"$url\" > \""+asked+"\"\n"+
				"while [ $# -gt 0 ]; do if [ \"$1\" = -D ]; then headers=$2; fi; shift; done\n"+
				"printf 'HTTP/1.1 "+code+"\\r\\n' > \"$headers\"\n"+
				"[ -n '"+c.held+"' ] && printf 'Docker-Content-Digest: "+c.held+"\\r\\n' >> \"$headers\"\n"+
				"printf '"+code+"'\n")
			for _, f := range []string{"make", "curl"} {
				if err := os.Chmod(filepath.Join(bin, f), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("sh", "-e", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TAG=v1.2.3", "TOKEN=t0k3n",
				"SERVER_URL="+c.serverURL, "REPOSITORY=Owner/Repo", "DIGEST="+c.digest)
			out, err := cmd.CombinedOutput()
			url, _ := os.ReadFile(asked)
			if strings.TrimSpace(string(url)) != c.wantAsks {
				t.Errorf("the step asked the registry %q; want %q:\n%s", strings.TrimSpace(string(url)), c.wantAsks, out)
			}
			if c.wantIMG == "" {
				if err == nil {
					t.Fatalf("the step passed with digest %q; want it to fail before make dist:\n%s", c.digest, out)
				}
				if _, statErr := os.Stat(got); statErr == nil {
					t.Errorf("make dist ran with digest %q:\n%s", c.digest, out)
				}
				if !strings.Contains(string(out), c.wantSays) {
					t.Errorf("the step's failure doesn't say %q:\n%s", c.wantSays, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("the step failed: %v\n%s", err, out)
			}
			args, readErr := os.ReadFile(got)
			if readErr != nil {
				t.Fatalf("make dist didn't run:\n%s", out)
			}
			want := "dist\nVERSION=v1.2.3\nIMG=" + c.wantIMG + "\n"
			if string(args) != want {
				t.Errorf("make received:\n%s\nwant:\n%s", args, want)
			}
		})
	}
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
		// #1587: a job's README applies, waits for and reruns a Job.
		"python job on kubernetes": {
			want: []string{
				"ships them through an `argoCD` DeployTarget",
				"`job.yaml` runs the placeholder image `sample-app:dev`",
				"kubectl wait --for=condition=complete job/sample-app",
				"kubectl logs job/sample-app",
				// #1628: the rerun recreates only the Job, not the whole kustomization.
				"kubectl delete job/sample-app\nkubectl apply -k deploy/k8s",
				"`job.yaml`: a Job that runs container `app` once to completion",
				"`argocd.argoproj.io/sync-options: Force=true,Replace=true`",
				"`connector-postgres.yaml`",
				"which the Job loads through `envFrom`",
				"runs to completion, so it has no port and no probes",
			},
			never: []string{"deployment.yaml", "rollout status", "service.yaml", "GET ", "the Deployment", "replace --force"},
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
			never: []string{"pyproject.toml", "package.json", "`image` job", "re-push the image"},
		},
		// #1583: an operator's guide says what the image job failing means,
		// that make dist failing leaves the image archive attached, and
		// that moving the tag does not re-push an image the registry holds.
		"kubebuilder operator": {
			want: []string{"`install.yaml` naming that image by digest",
				"runner labelled `image`", "a tag's image is never replaced",
				"- **The `image` job failed** (no `Dockerfile` at the root",
				"nothing was attached and no image was\n  pushed",
				"nothing was attached but the\n  image archive, which the factory pushes to the registry",
				"Moving the tag does not re-push the image",
				"`install.yaml` names the digest the registry holds",
				"make dist VERSION=v0.0.0-test\n"},
			never: []string{"pyproject.toml", "package.json", "nothing was attached."},
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

// TestGiteaReleaseGuideSeparatesLooseListItems checks that RELEASING.md's
// lists render Prettier-clean (#1707): an item that holds a fenced block
// makes its list loose, so ts-standard's Prettier check wants a blank line
// before the step that watches the run and before each item of "When a run
// fails". Every Framework's render is checked: python-cli's and go-cli's go
// unchecked by a Standard, and the operator's adds an item.
func TestGiteaReleaseGuideSeparatesLooseListItems(t *testing.T) {
	watch := regexp.MustCompile("may push `v\\*` tags\\.\\n\\n[34]\\. Watch the run")
	for _, c := range templateCombos {
		if !slices.ContainsFunc(c.layers, func(s side) bool { return s.Name == "gitea-release" }) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			_, result := renderCombo(t, c.typ, c.layers)
			f, ok := result.Files["RELEASING.md"]
			if !ok {
				t.Fatalf("RELEASING.md not written; files: %v", result.Files.Paths())
			}
			got := string(f.Data)
			if !watch.MatchString(got) {
				t.Errorf("RELEASING.md has no blank line before the step that watches the run (#1707):\n%s", got)
			}
			_, fails, ok := strings.Cut(got, "## When a run fails\n")
			if !ok {
				t.Fatalf("RELEASING.md has no \"When a run fails\" section:\n%s", got)
			}
			lines := strings.Split(fails, "\n")
			items := 0
			for i, l := range lines {
				if !strings.HasPrefix(l, "- **") {
					continue
				}
				items++
				if i == 0 || lines[i-1] != "" {
					t.Errorf("RELEASING.md has no blank line before %q (#1707):\n%s", l, fails)
				}
			}
			if items < 3 {
				t.Errorf("RELEASING.md's \"When a run fails\" lists %d items, want at least 3:\n%s", items, fails)
			}
		})
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

// TestNodeCLINamesItsPackageUnderTheScope: node-cli names its package
// <scope>/<container> in package.json and its lock, with the default scope
// and with the GitProvider's own (#1231). It stays private: that only stops
// npm publish to npmjs.org. npm packs a scoped package as
// <scope sans @>-<container>-<version>.tgz, and the README's install line
// and ts-standard's `make dist` help name that file, never the unscoped
// <container>-<version>.tgz (#1695).
func TestNodeCLINamesItsPackageUnderTheScope(t *testing.T) {
	for _, scope := range []string{"@c4factory", "@acme"} {
		t.Run(scope, func(t *testing.T) {
			in, layers := loadCombo(t, "cli", []side{
				{model.SideFramework, "node-cli"},
				{model.SideStandard, "ts-standard"},
				{model.SideDeployment, "gitea-release"},
			}, "")
			in.NpmScope = scope
			result, err := Build(in, layers, Options{})
			if err != nil {
				t.Fatal(err)
			}
			name := scope + "/sample-app"
			var pkg struct {
				Name    string            `json:"name"`
				Private bool              `json:"private"`
				Bin     map[string]string `json:"bin"`
			}
			if err := json.Unmarshal(result.Files["package.json"].Data, &pkg); err != nil {
				t.Fatal(err)
			}
			if pkg.Name != name || !pkg.Private || pkg.Bin["sample-app"] != "out/cli.js" {
				t.Errorf("package.json: name %q, private %v, bin %v; want %q, private, bin sample-app",
					pkg.Name, pkg.Private, pkg.Bin, name)
			}
			var lock struct {
				Name     string `json:"name"`
				Packages map[string]struct {
					Name string `json:"name"`
				} `json:"packages"`
			}
			if err := json.Unmarshal(result.Files["package-lock.json"].Data, &lock); err != nil {
				t.Fatal(err)
			}
			if lock.Name != name || lock.Packages[""].Name != name {
				t.Errorf("package-lock.json names %q and %q; want %q twice", lock.Name, lock.Packages[""].Name, name)
			}
			tarball := strings.TrimPrefix(scope, "@") + "-sample-app-<version>.tgz"
			for file, want := range map[string]string{
				"README.md": "npm install --global ./" + tarball,
				"Makefile":  "dist: release-version deps ## Pack dist/" + tarball,
			} {
				text := string(result.Files[file].Data)
				if !strings.Contains(text, want) {
					t.Errorf("%s lacks %q", file, want)
				}
				if strings.Contains(text, "/sample-app-<version>.tgz") {
					t.Errorf("%s names the unscoped sample-app-<version>.tgz; npm packs %s", file, tarball)
				}
			}
		})
	}
}

// TestStandardsCIWriteTheRegistryCredentials: the check workflows of
// ts-standard, python-standard and aws-standard and gitea-release's release
// workflow write ~/.npmrc and ~/.netrc from the owner's Actions variables
// and secrets, falling back to the host's own registries and gitea.token,
// before the step that installs (#1231). The step is one text in all four,
// and run under bash it names the index's host without its port (#1696).
func TestStandardsCIWriteTheRegistryCredentials(t *testing.T) {
	type workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				If   string            `yaml:"if"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	wantEnv := map[string]string{
		"NPM_SCOPE":    "${{ vars.C4_NPM_SCOPE }}",
		"NPM_REGISTRY": "${{ vars.C4_NPM_REGISTRY }}",
		"NPM_TOKEN":    "${{ secrets.C4_NPM_TOKEN }}",
		"PYPI_INDEX":   "${{ vars.C4_PYPI_INDEX }}",
		"PYPI_TOKEN":   "${{ secrets.C4_PYPI_TOKEN }}",
		"SERVER":       "${{ gitea.server_url }}",
		"OWNER":        "${{ gitea.repository_owner }}",
		"ACTOR":        "${{ gitea.actor }}",
		"TOKEN":        "${{ gitea.token }}",
	}
	wantRun := []string{
		`${NPM_REGISTRY:-$SERVER/api/packages/$OWNER/npm/}`,
		`${PYPI_INDEX:-$SERVER/api/packages/$OWNER/pypi/simple}`,
		`${NPM_SCOPE:-@c4factory}`, `${NPM_TOKEN:-$TOKEN}`, `${PYPI_TOKEN:-$TOKEN}`,
		"umask 077", `:_authToken=`, `> "$HOME/.npmrc"`, `> "$HOME/.netrc"`,
	}
	cases := []struct {
		name  string
		typ   string
		sides []side
		file  string
		// jobs maps a job to the step the credentials come before, and
		// whether the credentials step is gated on the change touching code.
		jobs map[string]struct {
			before string
			gated  bool
		}
	}{
		{"ts-standard", "cli", []side{{model.SideFramework, "node-cli"}, {model.SideStandard, "ts-standard"}},
			".gitea/workflows/check.yaml",
			map[string]struct {
				before string
				gated  bool
			}{"check": {"make check-light", true}, "full": {"make check", false}}},
		{"python-standard", "cli", []side{{model.SideFramework, "python-cli"}, {model.SideStandard, "python-standard"}},
			".gitea/workflows/check.yaml",
			map[string]struct {
				before string
				gated  bool
			}{"check": {"make check-light", true}, "full": {"make check", false}}},
		{"aws-standard", "function", []side{{model.SideFramework, "aws-lambda-ts"}, {model.SideStandard, "aws-standard"}},
			".gitea/workflows/check.yaml",
			map[string]struct {
				before string
				gated  bool
			}{"check": {"make check-light", true}, "full": {"make check", false}}},
		{"gitea-release", "cli", []side{{model.SideFramework, "node-cli"}, {model.SideStandard, "ts-standard"}, {model.SideDeployment, "gitea-release"}},
			".gitea/workflows/release.yaml",
			map[string]struct {
				before string
				gated  bool
			}{"release": {"make dist", false}}},
	}
	runs := map[string]string{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, result := renderComboChecks(t, c.typ, c.sides, "BeforeRelease")
			var w workflow
			if err := yaml.Unmarshal(result.Files[c.file].Data, &w); err != nil {
				t.Fatal(err)
			}
			for name, want := range c.jobs {
				job, ok := w.Jobs[name]
				if !ok {
					t.Fatalf("%s has no job %q", c.file, name)
				}
				wrote, installed := -1, -1
				for i, step := range job.Steps {
					if step.Name == "Credentials for the package registries" {
						wrote = i
						for k, v := range wantEnv {
							if step.Env[k] != v {
								t.Errorf("job %s: env %s=%q, want %q", name, k, step.Env[k], v)
							}
						}
						assertContains(t, "job "+name+"'s credentials step", step.Run, wantRun)
						if gated := step.If == "steps.changes.outputs.code == 'true'"; gated != want.gated {
							t.Errorf("job %s: if %q; want gated %v", name, step.If, want.gated)
						}
						runs[c.name+"/"+name] = step.Run
					}
					if strings.Contains(step.Run, want.before) {
						installed = i
					}
				}
				if wrote < 0 || installed < wrote {
					t.Errorf("job %s: the credentials step is #%d and %q #%d; want the credentials first",
						name, wrote, want.before, installed)
				}
			}
		})
	}
	const reference = "ts-standard/check"
	for name, run := range runs {
		if run != runs[reference] {
			t.Errorf("%s's credentials step differs from %s's:\n%s", name, reference, run)
		}
	}
	// The step under bash, as the runner runs it (#1696): ~/.netrc names the
	// index's host without the port, which uv, pip and requests match on;
	// the login is gitea.actor with the workflow's token and __token__ with
	// C4_PYPI_TOKEN; ~/.npmrc keeps the port, which npm matches on.
	const server = "http://gitea.gitea.svc.cluster.local:3000"
	for _, c := range []struct {
		name      string
		pypiToken string
		wantNetrc string
	}{
		{"the workflow's token", "", "machine gitea.gitea.svc.cluster.local\nlogin runner\npassword tok\n"},
		{"C4_PYPI_TOKEN", "pypi-tok", "machine gitea.gitea.svc.cluster.local\nlogin __token__\npassword pypi-tok\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := exec.Command("bash", "-euo", "pipefail", "-c", runs[reference])
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "SERVER=" + server,
				"OWNER=webshop", "ACTOR=runner", "TOKEN=tok", "PYPI_TOKEN=" + c.pypiToken}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the credentials step failed: %v\n%s", err, out)
			}
			netrc, err := os.ReadFile(filepath.Join(home, ".netrc"))
			if err != nil {
				t.Fatal(err)
			}
			if string(netrc) != c.wantNetrc {
				t.Errorf("~/.netrc:\n%s\nwant:\n%s", netrc, c.wantNetrc)
			}
			npmrc, err := os.ReadFile(filepath.Join(home, ".npmrc"))
			if err != nil {
				t.Fatal(err)
			}
			wantNpmrc := "@c4factory:registry=" + server + "/api/packages/webshop/npm/\n" +
				"//gitea.gitea.svc.cluster.local:3000/api/packages/webshop/npm/:_authToken=tok\n"
			if string(npmrc) != wantNpmrc {
				t.Errorf("~/.npmrc:\n%s\nwant:\n%s", npmrc, wantNpmrc)
			}
		})
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
		Description:    "Shows what a template renders.",
		Type:           typ,
		Checks:         checks,
		// The operator always renders the resolved block: the defaults when
		// the Container has none.
		Operations: model.ResolveOperations(model.OperationsChoices{}),
		CI:         giteaActions(t),
		NpmScope:   "@c4factory",
	}
	layers := make([]Layer, 0, len(sides))
	for _, l := range sides {
		files, err := ReadTree(os.DirFS(filepath.Join(templatesDir, l.Name)))
		if err != nil {
			t.Fatalf("read template %s: %v", l.Name, err)
		}
		name := l.Name
		if l.Side == model.SideDomain {
			// A Domain's template folder is <name>-domain; its catalog entry,
			// the layer and the directory it lands in carry the bare name.
			name = domainName(l.Name)
			in.Domains = append(in.Domains, name)
		}
		layers = append(layers, Layer{Side: l.Side, Name: name, Ref: "local", Files: files})
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

// TestGiteaReleasePinHoldsItsReleaseJobFixes checks that templates/versions.txt
// pins gitea-release at v1.4.1 or later: #1581's digest guard and #1583's
// registry lookup changed the release job under v1.4.0, and a tag never
// moves (the seed only warns), so a factory seeded at v1.4.0 kept the broken
// job (#1652).
func TestGiteaReleasePinHoldsItsReleaseJobFixes(t *testing.T) {
	versions, err := os.ReadFile(filepath.Join(templatesDir, "versions.txt"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := regexp.MustCompile(`(?m)^gitea-release v(\d+)\.(\d+)\.(\d+)$`).FindStringSubmatch(string(versions))
	if pinned == nil {
		t.Fatal("templates/versions.txt pins no gitea-release")
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(pinned[i+1])
	}
	if slices.Compare(v[:], []int{1, 4, 1}) < 0 {
		t.Errorf("templates/versions.txt pins gitea-release v%d.%d.%d; the release job's fixes for #1581 and #1583 need v1.4.1 or later (#1652)",
			v[0], v[1], v[2])
	}
}

// #1579: go-kubebuilder's Dockerfile cross-builds by itself, so the
// Standard's docker-buildx builds it as it is. kubebuilder's sed put a second
// --platform on its first FROM, which BuildKit refuses, and the -prefixed
// build line then exited 0 having pushed nothing.
func TestOperatorStandardDockerBuildxUsesTheDockerfile(t *testing.T) {
	_, result := renderCombo(t, "operator", []side{
		{model.SideFramework, "go-kubebuilder"},
		{model.SideStandard, "go-operator-standard"},
	})
	f, ok := result.Files["Makefile"]
	if !ok {
		t.Fatalf("Makefile not written for an operator; files: %v", result.Files.Paths())
	}
	makefile := string(f.Data)
	start := strings.Index(makefile, "\ndocker-buildx:")
	if start < 0 {
		t.Fatalf("operator Makefile has no docker-buildx target:\n%s", makefile)
	}
	recipe := makefile[start+1:]
	if end := strings.Index(recipe, "\n\n"); end >= 0 {
		recipe = recipe[:end]
	}
	for _, banned := range []string{"Dockerfile.cross", "sed ", "--platform=\\$$"} {
		if strings.Contains(recipe, banned) {
			t.Errorf("docker-buildx mentions %q (#1579):\n%s", banned, recipe)
		}
	}
	if !strings.Contains(recipe, "-f Dockerfile .") {
		t.Errorf("docker-buildx does not build -f Dockerfile (#1579):\n%s", recipe)
	}
	for line := range strings.SplitSeq(recipe, "\n") {
		if strings.Contains(line, "buildx build") && strings.HasPrefix(strings.TrimSpace(line), "-") {
			t.Errorf("docker-buildx ignores a failed build (#1579): %q", line)
		}
	}
}

// #1779: the Makefile runs recipes with -e, so a failed buildx build exited
// before the `buildx rm` after it and left the builder behind. Run the real
// recipe with a stand-in container tool: the builder goes whether the build
// fails or not, and a failed build still fails the target.
func TestOperatorStandardDockerBuildxRemovesTheBuilder(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not on PATH")
	}
	_, result := renderCombo(t, "operator", []side{
		{model.SideFramework, "go-kubebuilder"},
		{model.SideStandard, "go-operator-standard"},
	})
	f, ok := result.Files["Makefile"]
	if !ok {
		t.Fatalf("Makefile not written for an operator; files: %v", result.Files.Paths())
	}
	for _, c := range []struct {
		name      string
		buildExit int
	}{
		{"failed build", 3},
		{"good build", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "Makefile"), f.Data, 0o644); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "calls")
			tool := filepath.Join(dir, "tool")
			script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %q\nif [ \"$2\" = build ]; then exit %d; fi\n", log, c.buildExit)
			if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("make", "-s", "docker-buildx", "CONTAINER_TOOL="+tool, "IMG=example/op:1")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if c.buildExit != 0 && err == nil {
				t.Errorf("docker-buildx exited 0 on a failed build (#1779):\n%s", out)
			}
			if c.buildExit == 0 && err != nil {
				t.Errorf("docker-buildx failed on a good build: %v\n%s", err, out)
			}
			calls, _ := os.ReadFile(log)
			removed := 0
			for line := range strings.SplitSeq(string(calls), "\n") {
				if strings.HasPrefix(line, "buildx rm ") && strings.HasSuffix(line, "-builder") {
					removed++
				}
			}
			if removed != 1 {
				t.Errorf("docker-buildx ran buildx rm %d times, want 1 (#1779); calls:\n%s\nmake:\n%s", removed, calls, out)
			}
		})
	}
}

// TestFactorySelfWritesItsCheckAsIs checks that factory-self writes one file,
// its Gitea workflow, with the template's bytes (it is no .tmpl), and that the
// workflow's one job is check and runs make check (#1890).
func TestFactorySelfWritesItsCheckAsIs(t *testing.T) {
	const workflow = ".gitea/workflows/check.yaml"
	layers, result := renderCombo(t, "operator", []side{
		{model.SideFramework, "go-kubebuilder"},
		{model.SideStandard, "factory-self"},
	})
	src := ciDir + "giteaActions/" + workflow
	if got := layers[1].Files.Paths(); !slices.Equal(got, []string{src}) {
		t.Errorf("factory-self holds %v, want only %s", got, src)
	}
	got, ok := result.Files[workflow]
	if !ok {
		t.Fatalf("%s not written", workflow)
	}
	if want := layers[1].Files[src].Data; !bytes.Equal(got.Data, want) {
		t.Errorf("%s differs from the template's bytes:\n%s", workflow, got.Data)
	}
	var wf struct {
		Jobs map[string]struct {
			RunsOn string `yaml:"runs-on"`
			Steps  []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(got.Data, &wf); err != nil {
		t.Fatal(err)
	}
	if len(wf.Jobs) != 1 {
		t.Fatalf("%s has jobs %v, want only check", workflow, wf.Jobs)
	}
	check, ok := wf.Jobs["check"]
	if !ok || check.RunsOn != "go" || len(check.Steps) == 0 || check.Steps[len(check.Steps)-1].Run != "make check" {
		t.Errorf("%s's job check = %+v, want it to run on go and end with make check", workflow, check)
	}
}

// domainSuffix ends every Domain template's folder under templates/; the
// catalog entry is the folder's name without it (webshop-domain is the Domain
// webshop).
const domainSuffix = "-domain"

func domainName(folder string) string { return strings.TrimSuffix(folder, domainSuffix) }

// TestDomainTemplatesHaveTheShape checks every Domain template in templates/
// against docs/plans/domains.md, decision 7: Markdown only, a README.md whose
// topic list names every other file, and in each topic file the four
// headings Practices, Common mistakes, Controversies and Sources. The
// template's own .c4factory/ notes are not rendered and are left out. Built
// as the Domain of a Container, every file lands under
// docs/agents/domain/<name>/ (#1759).
func TestDomainTemplatesHaveTheShape(t *testing.T) {
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatal(err)
	}
	headings := []string{"## Practices", "## Common mistakes", "## Controversies", "## Sources"}
	var found int
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), domainSuffix) {
			continue
		}
		found++
		t.Run(e.Name(), func(t *testing.T) {
			files, err := ReadTree(os.DirFS(filepath.Join(templatesDir, e.Name())))
			if err != nil {
				t.Fatal(err)
			}
			readme, ok := files["README.md"]
			if !ok {
				t.Fatal("no README.md: the index the Standards' guides point at")
			}
			var topics []string
			for _, p := range files.Paths() {
				if strings.HasPrefix(p, metaDir) {
					continue
				}
				rendered := strings.TrimSuffix(p, templateSuffix)
				if !strings.HasSuffix(rendered, ".md") {
					t.Errorf("%s is not Markdown; a Domain holds *.md and *.md.tmpl only", p)
					continue
				}
				if rendered == "README.md" {
					continue
				}
				topics = append(topics, rendered)
				if !bytes.Contains(readme.Data, []byte("`"+rendered+"`")) {
					t.Errorf("README.md doesn't name %s; it lists one line per topic file", rendered)
				}
				for _, h := range headings {
					if !bytes.Contains(files[p].Data, []byte("\n"+h+"\n")) {
						t.Errorf("%s lacks the heading %q", p, h)
					}
				}
			}
			if len(topics) == 0 {
				t.Error("no topic file next to README.md")
			}

			name := domainName(e.Name())
			in := withDomains(ordersAPI, name)
			layers := append(webshopLayers(), Layer{Side: model.SideDomain, Name: name, Ref: "local", Files: files})
			res, err := Build(in, layers, Options{})
			if err != nil {
				t.Fatalf("Build() with the Domain: %v", err)
			}
			dir := model.DomainDir + name + "/"
			for _, p := range append(topics, "README.md") {
				if _, ok := res.Files[dir+p]; !ok {
					t.Errorf("Build() didn't write %s", dir+p)
				}
			}
		})
	}
	if found == 0 {
		t.Fatalf("no *%s folder under %s; the webshop Domain is templates/webshop-domain", domainSuffix, templatesDir)
	}
}
