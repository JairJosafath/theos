package ports

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitea is the word the host-neutral guard counts, case-insensitively, so it
// matches `Gitea`, `GITEA_` and `.gitea/` alike.
const gitea = "gitea"

// giteaAllowlist holds the Go files outside internal/adapters/gitea that name
// Gitea today, each with its count (#1172). It may only shrink: a change that
// removes hits lowers its entry, and one that drops a file to none removes it.
var giteaAllowlist = map[string]int{
	"api/v1alpha1/brief_types.go":                                 1,
	"api/v1alpha1/deploytarget_types.go":                          1,
	"api/v1alpha1/framework_types.go":                             1,
	"api/v1alpha1/gitprovider_types.go":                           17,
	"api/v1alpha1/standard_types.go":                              1,
	"api/v1alpha1/zz_generated.deepcopy.go":                       10,
	"api/v1beta1/brief_types.go":                                  1,
	"api/v1beta1/deploytarget_types.go":                           1,
	"api/v1beta1/framework_types.go":                              1,
	"api/v1beta1/gitprovider_types.go":                            17,
	"api/v1beta1/standard_types.go":                               1,
	"api/v1beta1/zz_generated.deepcopy.go":                        10,
	"cmd/main.go":                                                 3,
	"hack/render/main.go":                                         2,
	"hack/runner-fence/main.go":                                   31,
	"hack/runner-fence/main_test.go":                              63,
	"hack/template-go-kubebuilder/main.go":                        1,
	"internal/adapters/argocd/deployer_remote_test.go":            4,
	"internal/adapters/argocd/deployer_test.go":                   1,
	"internal/adapters/cloudformation/deployer_test.go":           3,
	"internal/adapters/git/git_update_test.go":                    1,
	"internal/agentrunner/agentrunner_test.go":                    15,
	"internal/agentrunner/netrc_test.go":                          16,
	"internal/agentrunner/npmrc_test.go":                          8,
	"internal/agentrunner/pause_test.go":                          1,
	"internal/agentrunner/plan_test.go":                           3,
	"internal/agentrunner/retry.go":                               1,
	"internal/agentrunner/retry_test.go":                          11,
	"internal/agentrunner/run.go":                                 1,
	"internal/agentrunner/score_test.go":                          2,
	"internal/agentrunner/spec_test.go":                           1,
	"internal/agentrunner/triage_test.go":                         1,
	"internal/agentrunner/verify_test.go":                         2,
	"internal/controller/brief_apps.go":                           1,
	"internal/controller/brief_controller_test.go":                11,
	"internal/controller/brief_failed_test.go":                    3,
	"internal/controller/brief_order_test.go":                     2,
	"internal/controller/brief_release_test.go":                   3,
	"internal/controller/brief_request_released_test.go":          3,
	"internal/controller/brief_round_test.go":                     5,
	"internal/controller/brief_round_unit_test.go":                1,
	"internal/controller/brief_suggestions_test.go":               6,
	"internal/controller/brief_uses_test.go":                      4,
	"internal/controller/catalog_test.go":                         7,
	"internal/controller/ci_runner_pod.go":                        2,
	"internal/controller/ci_runner_pod_unit_test.go":              20,
	"internal/controller/container_adopt_unit_test.go":            1,
	"internal/controller/container_deploy.go":                     4,
	"internal/controller/container_deploy_argocd_test.go":         2,
	"internal/controller/container_deploy_cloudformation_test.go": 5,
	"internal/controller/container_deploy_gitlab_test.go":         6,
	"internal/controller/container_deploy_test.go":                1,
	"internal/controller/container_image_test.go":                 8,
	"internal/controller/container_operations_test.go":            1,
	"internal/controller/container_operations_unit_test.go":       1,
	"internal/controller/container_scaffold.go":                   1,
	"internal/controller/container_scaffold_test.go":              6,
	"internal/controller/container_standard_checks_test.go":       1,
	"internal/controller/container_standard_test.go":              2,
	"internal/controller/container_standard_unit_test.go":         7,
	"internal/controller/conversion_test.go":                      1,
	"internal/controller/crew_bump_test.go":                       1,
	"internal/controller/crew_controller.go":                      1,
	"internal/controller/crew_full_check_test.go":                 4,
	"internal/controller/crew_packages_test.go":                   4,
	"internal/controller/crew_publish.go":                         40,
	"internal/controller/crew_publish_source_test.go":             2,
	"internal/controller/crew_publish_test.go":                    2,
	"internal/controller/crew_publish_unit_test.go":               3,
	"internal/controller/crew_release.go":                         1,
	"internal/controller/crew_release_source.go":                  2,
	"internal/controller/crew_release_test.go":                    3,
	"internal/controller/domain_test.go":                          1,
	"internal/controller/gitprovider_packages_test.go":            1,
	"internal/controller/helpers_test.go":                         1,
	"internal/controller/project_ci_runners.go":                   4,
	"internal/controller/project_ci_runners_test.go":              33,
	"internal/controller/project_controller_test.go":              6,
	"internal/controller/project_packages_test.go":                7,
	"internal/controller/relationship_controller_test.go":         1,
	"internal/controller/sandbox_fence_unit_test.go":              7,
	"internal/controller/standard_test.go":                        1,
	"internal/controller/task_actions.go":                         1,
	"internal/controller/task_checks_test.go":                     11,
	"internal/controller/task_continue_test.go":                   1,
	"internal/controller/task_controller_test.go":                 7,
	"internal/controller/task_issue_unit_test.go":                 2,
	"internal/controller/task_plan_test.go":                       2,
	"internal/controller/task_sandbox_unit_test.go":               24,
	"internal/controller/task_spec_test.go":                       2,
	"internal/controller/task_standard_test.go":                   6,
	"internal/model/autopilot_test.go":                            5,
	"internal/model/aws_data_store_test.go":                       1,
	"internal/model/aws_ec2_test.go":                              2,
	"internal/model/aws_event_test.go":                            2,
	"internal/model/aws_queue_test.go":                            2,
	"internal/model/aws_schedule_test.go":                         2,
	"internal/model/aws_service_test.go":                          26,
	"internal/model/aws_static_site_test.go":                      2,
	"internal/model/brief_behind_test.go":                         2,
	"internal/model/brief_checks_test.go":                         1,
	"internal/model/brief_parallel_test.go":                       1,
	"internal/model/brief_request_round_test.go":                  3,
	"internal/model/brief_request_test.go":                        14,
	"internal/model/brief_standin.go":                             1,
	"internal/model/brief_standin_test.go":                        19,
	"internal/model/brief_test.go":                                11,
	"internal/model/brief_workspace.go":                           3,
	"internal/model/brief_workspace_test.go":                      13,
	"internal/model/bump_test.go":                                 2,
	"internal/model/bundle_test.go":                               2,
	"internal/model/byhand_test.go":                               3,
	"internal/model/cdk_test.go":                                  1,
	"internal/model/ci.go":                                        15,
	"internal/model/ci_test.go":                                   19,
	"internal/model/context_test.go":                              3,
	"internal/model/deploy.go":                                    3,
	"internal/model/deploy_argocd_remote.go":                      1,
	"internal/model/deploy_argocd_remote_test.go":                 13,
	"internal/model/deploy_github_test.go":                        5,
	"internal/model/deploy_test.go":                               4,
	"internal/model/failed_test.go":                               3,
	"internal/model/fence_test.go":                                7,
	"internal/model/full_check_test.go":                           6,
	"internal/model/function_type_test.go":                        4,
	"internal/model/image.go":                                     7,
	"internal/model/image_test.go":                                12,
	"internal/model/model.go":                                     12,
	"internal/model/model_test.go":                                9,
	"internal/model/operationsupdate_test.go":                     4,
	"internal/model/planner_guide_test.go":                        2,
	"internal/model/planner_operator_test.go":                     3,
	"internal/model/publish.go":                                   23,
	"internal/model/publish_check_test.go":                        2,
	"internal/model/publish_source_test.go":                       16,
	"internal/model/publish_test.go":                              41,
	"internal/model/python_job_test.go":                           2,
	"internal/model/spec_test.go":                                 3,
	"internal/model/standard_test.go":                             19,
	"internal/model/standardupdate_test.go":                       2,
	"internal/model/suggestion_test.go":                           11,
	"internal/model/task.go":                                      14,
	"internal/model/task_test.go":                                 26,
	"internal/ports/agentaccess.go":                               1,
	"internal/ports/fake/ci.go":                                   1,
	"internal/ports/fake/git.go":                                  5,
	"internal/ports/fake/git_test.go":                             2,
	"internal/ports/fake/githost_test.go":                         2,
	"internal/ports/fake/issues.go":                               6,
	"internal/ports/fake/issues_test.go":                          1,
	"internal/ports/fake/pulls.go":                                1,
	"internal/ports/fake/registry.go":                             6,
	"internal/ports/fake/releases.go":                             2,
	"internal/ports/fake/runners.go":                              1,
	"internal/ports/fake/runners_test.go":                         3,
	"internal/ports/githost.go":                                   3,
	"internal/ports/githost_test.go":                              9,
	"internal/scaffold/check_skip_test.go":                        2,
	"internal/scaffold/checks_test.go":                            3,
	"internal/scaffold/ci_test.go":                                30,
	"internal/scaffold/scaffold.go":                               1,
	"internal/scaffold/templates_test.go":                         101,
	"internal/scaffold/update_test.go":                            8,
	"test/chart/catalog_test.go":                                  18,
	"test/chart/chat_test.go":                                     1,
	"test/chart/gitprovider_pool_test.go":                         12,
	"test/chart/gitproviders_test.go":                             3,
	"test/chart/keep_secrets_test.go":                             40,
	"test/chart/packages_test.go":                                 10,
	"test/chart/sample_releases_test.go":                          6,
	"test/chart/sample_webshop_test.go":                           1,
	"test/chart/verify_release_guide_test.go":                     7,
	"test/e2e/agents_test.go":                                     8,
	"test/e2e/brief_test.go":                                      4,
	"test/e2e/e2e_suite_test.go":                                  10,
	"test/e2e/helpers_test.go":                                    14,
	"test/e2e/standin/standin_test.go":                            1,
}

// TestHostNeutral is the host-neutral guard (docs/architecture.md, "Ports"):
// outside the Gitea adapter, no Go file names Gitea more often than its
// allowlist entry, and no entry is above its file's count (#1172).
func TestHostNeutral(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	counts, err := countGitea(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range checkGitea(counts, giteaAllowlist) {
		t.Error(msg)
	}
}

// countGitea counts the gitea hits in each Go file under root, by its
// slash-separated path from root, skipping the Gitea adapter, this test, and
// directories no Go build reads: hidden ones, bin, testbin, vendor and
// node_modules.
func countGitea(root string) (map[string]int, error) {
	counts := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (strings.HasPrefix(name, ".") || name == "bin" || name == "testbin" ||
				name == "vendor" || name == "node_modules" || rel == "internal/adapters/gitea") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || rel == "internal/ports/hostneutral_test.go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if n := strings.Count(strings.ToLower(string(b)), gitea); n > 0 {
			counts[rel] = n
		}
		return nil
	})
	return counts, err
}

// checkGitea compares the counts with the allowlist and returns one message
// per file that breaks it, sorted by path.
func checkGitea(counts, allow map[string]int) []string {
	const fix = "; reach Gitea through a port in internal/ports and its adapter in internal/adapters/gitea"
	var msgs []string
	for file, n := range counts {
		want, ok := allow[file]
		switch {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("%s: names gitea %d times and is not in giteaAllowlist%s", file, n, fix))
		case n > want:
			msgs = append(msgs, fmt.Sprintf("%s: names gitea %d times, giteaAllowlist allows %d%s", file, n, want, fix))
		}
	}
	for file, want := range allow {
		if n := counts[file]; n < want {
			msgs = append(msgs, fmt.Sprintf("%s: names gitea %d times, below its giteaAllowlist entry of %d; "+
				"lower the entry to %d, or remove it at 0: the list only shrinks", file, n, want, n))
		}
	}
	slices.Sort(msgs)
	return msgs
}

func TestCheckGitea(t *testing.T) {
	allow := map[string]int{"a.go": 2, "b.go": 1}
	tests := []struct {
		name   string
		counts map[string]int
		want   []string
	}{
		{"at the allowlist", map[string]int{"a.go": 2, "b.go": 1}, nil},
		{"a new file", map[string]int{"a.go": 2, "b.go": 1, "c.go": 1},
			[]string{"c.go: names gitea 1 times and is not in giteaAllowlist"}},
		{"one more in a listed file", map[string]int{"a.go": 3, "b.go": 1},
			[]string{"a.go: names gitea 3 times, giteaAllowlist allows 2"}},
		{"fewer than listed", map[string]int{"a.go": 1},
			[]string{"a.go: names gitea 1 times, below its giteaAllowlist entry of 2", "b.go: names gitea 0 times"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkGitea(tt.counts, allow)
			if len(got) != len(tt.want) {
				t.Fatalf("checkGitea = %q, want %d messages", got, len(tt.want))
			}
			for i, prefix := range tt.want {
				if !strings.HasPrefix(got[i], prefix) {
					t.Errorf("message %d = %q, want it to start %q", i, got[i], prefix)
				}
			}
		})
	}
}
