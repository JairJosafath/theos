package awsenv

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A machine that runs one CI job (docs/plans/test-environments.md, step 8):
// leased, fenced, tagged and returned as any lease's machine, but its user
// data starts the CI runner the CI registered for that job instead of
// serving an agent's runs. It serves theos-env only until the operator,
// finding the lease's own theos-env there, has turned the metadata service
// off; the runner starts after, so the job never reads the machine's user
// data, and the machine shuts down, which terminates it, when the runner
// exits after its one job, or at the lease's deadline.

var _ ports.TestEnvironmentRunners = (*Provider)(nil)

// WithRunnerURL makes p's machines that run a CI job download the CI runner
// from url, an https:// address in which ${arch} becomes amd64 or arm64,
// with a SHA256SUMS next to it, as the tools URL; "" starts no runner. It
// returns p.
func (p *Provider) WithRunnerURL(url string) *Provider {
	p.runnerURL = url
	return p
}

// RunnerMissing says why p starts no CI runner: it has no address to
// download one from.
func (p *Provider) RunnerMissing() string {
	if p.runnerURL == "" {
		return "the operator has no address to download the CI runner from: set its --test-environment-runner-url"
	}
	return ""
}

// LeaseRunner is Lease of a machine that runs runner.
func (p *Provider) LeaseRunner(ctx context.Context, req ports.LeaseRequest, runner ports.JobRunner) (ports.Lease, error) {
	if why := p.RunnerMissing(); why != "" {
		return ports.Lease{}, errors.New(why)
	}
	return p.lease(ctx, req, &runner)
}

// launchInput is the RunInstances call of req's machine, one that runs
// runner when it is set.
func (p *Provider) launchInput(req ports.LeaseRequest, runner *ports.JobRunner, group string,
	microUSDPerHour int64) (*ec2.RunInstancesInput, error) {
	if runner == nil {
		return LaunchInput(req, group, microUSDPerHour, p.toolsURL, p.now())
	}
	return RunnerLaunchInput(req, *runner, group, microUSDPerHour, p.toolsURL, p.runnerURL, p.now())
}

// RunnerLaunchInput is LaunchInput of a machine that runs runner: fenced
// and tagged the same, the lease's holder the runner (model.CIHolder), with
// RunnerUserData's script.
func RunnerLaunchInput(req ports.LeaseRequest, runner ports.JobRunner, group string, microUSDPerHour int64,
	toolsURL, runnerURL string, now time.Time) (*ec2.RunInstancesInput, error) {
	in, err := LaunchInput(req, group, microUSDPerHour, toolsURL, now)
	if err != nil {
		return nil, err
	}
	deadline := req.Deadline
	if deadline <= 0 {
		deadline = model.LeaseDeadline(0)
	}
	script, err := RunnerUserData(toolsURL, runnerURL, req.Token, runner, deadline)
	if err != nil {
		return nil, err
	}
	in.UserData = aws.String(base64.StdEncoding.EncodeToString([]byte(script)))
	return in, nil
}

// maxUserData is how long EC2 takes user data to be, before its base64.
const maxUserData = 16 << 10

// runnerFileName and runnerArg are what a runner's file names and
// arguments may be: nothing the shell reads as more than one word.
var (
	runnerFileName = regexp.MustCompile(`^[.A-Za-z0-9_-]+$`)
	runnerArg      = regexp.MustCompile(`^[A-Za-z0-9_./=:,+-]+$`)
)

// RunnerUserData is the script a machine that runs runner runs at its first
// boot: shut down at the lease's deadline; download theos-env from toolsURL
// and the CI runner from runnerURL, each checked against the SHA256SUMS
// next to it; write runner's files into model.RemoteRunnerDir, readable by
// root alone; serve theos-env with the lease's token until the metadata
// service stops answering, which the operator turns off once it answered;
// then run the runner with its arguments there, and shut down when it
// exits. It holds the runner's own credential, which is good for its one
// job, and never the CI's registration token.
func RunnerUserData(toolsURL, runnerURL, token string, runner ports.JobRunner, deadline time.Duration) (string, error) {
	tools, err := downloadAddress(toolsURL)
	if err != nil {
		return "", err
	}
	runnerAt, err := downloadAddress(runnerURL)
	if err != nil {
		return "", err
	}
	if !model.LeaseTokenSafe(token) {
		return "", errors.New("a lease's token must be letters, digits, '-', '_' and '.'")
	}
	if len(runner.Files) == 0 || len(runner.Args) == 0 {
		return "", fmt.Errorf("CI runner %q has no files or no arguments to run with", runner.Name)
	}
	names := make([]string, 0, len(runner.Files))
	for name := range runner.Files {
		if !runnerFileName.MatchString(name) || name == "." || name == ".." {
			return "", fmt.Errorf("CI runner file %q is no plain file name", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	var files strings.Builder
	for _, name := range names {
		fmt.Fprintf(&files, "printf '%%s' '%s' | base64 -d >%s/%s\n",
			base64.StdEncoding.EncodeToString(runner.Files[name]), model.RemoteRunnerDir, name)
	}
	for _, arg := range runner.Args {
		if !runnerArg.MatchString(arg) {
			return "", fmt.Errorf("CI runner argument %q holds a character the shell would read", arg)
		}
	}
	minutes := int64(math.Ceil(deadline.Minutes()))
	r := strings.NewReplacer("@MINUTES@", strconv.FormatInt(minutes, 10), "@TOOLS@", tools, "@RUNNER@", runnerAt,
		"@DIR@", model.RemoteRunnerDir, "@FILES@", strings.TrimSuffix(files.String(), "\n"), "@TOKEN@", token,
		"@PORT@", strconv.Itoa(model.TestEnvRemotePort), "@ARGS@", strings.Join(runner.Args, " "))
	script := r.Replace(runnerUserDataScript)
	if len(script) > maxUserData {
		return "", fmt.Errorf("the CI runner's user data is %d bytes, more than EC2's %d", len(script), maxUserData)
	}
	return script, nil
}

// downloadAddress is url as the user data's shell reads it: single-quoted,
// so it expands nothing but ${arch}.
func downloadAddress(url string) (string, error) {
	if !strings.HasPrefix(url, "https://") || strings.ContainsAny(url, "'\n\r\\") {
		return "", fmt.Errorf("the download address %q must be https:// with no quote, newline or backslash", url)
	}
	if before, after, ok := strings.Cut(url, "${arch}"); ok {
		return "'" + before + `'"$arch"'` + after + "'", nil
	}
	return "'" + url + "'", nil
}

const runnerUserDataScript = `#!/bin/sh
# A theos test environment's machine that runs one CI job
# (docs/plans/test-environments.md, step 8): it serves theos-env until the
# operator, finding the lease's own there, has turned the metadata service
# off; then the CI runner registered for the job takes it, and the machine
# shuts down, which terminates it, when the runner exits, or at the lease's
# deadline.
set -eu
shutdown -h +@MINUTES@ || true
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*)
	echo "theos-env: no build for $(uname -m)" >&2
	exit 1
	;;
esac
# fetch installs $1 as $2 once it matches the SHA256SUMS next to it.
fetch() {
	name=${1##*/}
	dir=$(mktemp -d)
	curl -fsSL --retry 5 -o "$dir/$name" "$1"
	curl -fsSL --retry 5 -o "$dir/SHA256SUMS" "${1%/*}/SHA256SUMS"
	(cd "$dir" && grep " \*\{0,1\}$name\$" SHA256SUMS | sha256sum -c -)
	install -m 0755 "$dir/$name" "$2"
	rm -rf "$dir"
}
fetch @TOOLS@ /usr/local/bin/theos-env
fetch @RUNNER@ /usr/local/bin/theos-ci-runner
umask 077
mkdir -p /work @DIR@
@FILES@
cat >/usr/local/sbin/theos-ci-job <<'EOF'
#!/bin/sh
# The job's runner starts once the metadata service, which holds this
# machine's user data, stops answering three times in a row.
THEOS_ENV_TOKEN=@TOKEN@ /usr/local/bin/theos-env serve --listen :@PORT@ --work /work --tls &
serve=$!
off=0
while [ "$off" -lt 3 ]; do
	if curl -fs -m 2 -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 30' -o /dev/null \
		http://169.254.169.254/latest/api/token; then
		off=0
	else
		off=$((off + 1))
	fi
	sleep 5
done
kill "$serve" || true
cd @DIR@
/usr/local/bin/theos-ci-runner @ARGS@ || true
shutdown -h now
EOF
chmod 0700 /usr/local/sbin/theos-ci-job
if command -v systemd-run >/dev/null 2>&1; then
	systemd-run --unit=theos-ci-job /usr/local/sbin/theos-ci-job
else
	nohup /usr/local/sbin/theos-ci-job >/var/log/theos-ci-job.log 2>&1 &
fi
`
