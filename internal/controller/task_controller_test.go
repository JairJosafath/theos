/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// newAgent returns the Agent "claude" with the given number of rounds.
func newAgent(ns string, rounds int32) *c4v1beta1.Agent {
	return &c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "claude"),
		Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev", MaxRounds: rounds}}
}

// newTask returns a Task on the Container orders-api.
func newTask(ns, name, goal string) *c4v1beta1.Task {
	return &c4v1beta1.Task{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.TaskSpec{
		ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"},
		AgentRef:     c4v1beta1.LocalRef{Name: "claude"},
		Goal:         goal,
	}}
}

// readyContainer creates the factory, a Standard and the Container orders-api with
// it, and waits until the Container is Ready.
func readyContainer(ns string) {
	const name = "orders-api"
	GinkgoHelper()
	createFactory(ns)
	container := newContainer(ns, name, "webshop-core", c4v1beta1.ContainerTypeService)
	container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-strict"}
	standard := newStandard(ns, "python-strict", "python-fastapi")
	standard.Spec.ProtectedPaths = []string{"Makefile", "ruff.toml"}
	mustCreate(newSystem(ns, "webshop-core"), standard, container)
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, name), container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
	}).Should(Succeed())
}

func getTask(g Gomega, ns, name string) *c4v1beta1.Task {
	task := &c4v1beta1.Task{}
	g.Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
	return task
}

// sandboxExists reports whether the operator created a Sandbox.
func sandboxExists(g Gomega, ns, name string) *unstructured.Unstructured {
	sandbox := newSandbox()
	g.Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed())
	return sandbox
}

// expectSandboxGone waits for the operator to delete a run's Sandbox: it
// writes the Task's status before it deletes the run, on purpose (the run's
// result is saved before its sandbox goes), so a loaded API server can show
// the status first (#1504, #1293).
func expectSandboxGone(ns, name string) {
	GinkgoHelper()
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, name), newSandbox()))
	}).Should(BeTrue(), "Sandbox %s is deleted", name)
}

// finishSandbox plays Agent Sandbox and theos-agent: it creates the sandbox's pod,
// owned by the sandbox, and ends it with result as termination message, or with
// reason and no message. Its writes are tried again: a loaded API server can
// time a request out (#829), and nothing else writes the pod.
func finishSandbox(ns, name, task string, result *agentrunner.Result, reason string) {
	GinkgoHelper()
	sandbox := newSandbox()
	Eventually(func(g Gomega) { g.Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed()) }).Should(Succeed())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{LabelTask: task},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent"}}},
	}
	Eventually(func() error { return client.IgnoreAlreadyExists(k8sClient.Create(ctx, pod.DeepCopy())) }).
		Should(Succeed())
	terminated := &corev1.ContainerStateTerminated{Reason: reason, ExitCode: 0}
	phase := corev1.PodSucceeded
	if result != nil {
		data, err := json.Marshal(result)
		Expect(err).NotTo(HaveOccurred())
		terminated.Message = string(data)
		terminated.Reason = "Completed"
	} else {
		phase, terminated.ExitCode = corev1.PodFailed, 137
	}
	status := corev1.PodStatus{Phase: phase, ContainerStatuses: []corev1.ContainerStatus{{
		Name: "agent", Image: "agent", State: corev1.ContainerState{Terminated: terminated},
	}}}
	Eventually(func(g Gomega) {
		got := &corev1.Pod{}
		g.Expect(k8sClient.Get(ctx, key(ns, name), got)).To(Succeed())
		g.Expect(got.OwnerReferences).To(ContainElement(HaveField("UID", sandbox.GetUID())),
			"a pod of an earlier sandbox with this name")
		got.Status = status
		g.Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())
	}).Should(Succeed())
}

var _ = Describe("Task", func() {
	It("runs rounds of implement, check and review until it merges", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop\n\nWith an aspect lock."))

		By("starting the implementer in a sandbox without cluster credentials")
		var sandbox *unstructured.Unstructured
		Eventually(func(g Gomega) {
			sandbox = sandboxExists(g, ns, "add-crop-r1-implement")
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Branch).To(Equal("task/add-crop"))
			g.Expect(task.OwnerReferences).To(ContainElement(HaveField("Kind", "Container")))
		}).Should(Succeed())
		spec, _, _ := unstructured.NestedMap(sandbox.Object, "spec", "podTemplate", "spec")
		Expect(spec["automountServiceAccountToken"]).To(BeFalse())
		Expect(spec["restartPolicy"]).To(Equal("Never"))
		containers, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "podTemplate", "spec", "containers")
		Expect(containers).To(HaveLen(1))
		env := containers[0].(map[string]any)["env"]
		Expect(env).To(ContainElements(
			map[string]any{"name": "THEOS_ROUND", "value": "1"},
			map[string]any{"name": "THEOS_MAX_ROUNDS", "value": "3"},
			map[string]any{"name": "THEOS_BRANCH", "value": "task/add-crop"},
			map[string]any{"name": "THEOS_ROLE", "value": "implement"},
		))
		Expect(env).To(ContainElement(map[string]any{"name": "THEOS_PROTECTED_PATHS",
			"value": ".devcontainer/,.gitea/,.theos.yaml,Makefile,ruff.toml"}))
		rule, tags := gitFake.BranchRule(ns, "orders-api")
		Expect(rule.ProtectedPaths).To(Equal([]string{".devcontainer/", ".gitea/", ".theos.yaml", "Makefile", "ruff.toml"}),
			"the Git host refuses changes to the Standard's and the platform's files")
		Expect(tags).To(Equal([]string{"v*"}), "only the operator tags releases")
		Expect(sandbox.GetOwnerReferences()).To(ContainElement(HaveField("Kind", "Task")))
		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-agent-git"), &secret)).To(Succeed())
		Expect(string(secret.Data["username"])).To(Equal(ns + "-orders-api-agent"))
		Expect(string(secret.Data["scopes"])).To(Equal(ports.AgentTokenScopes))
		Expect(env).To(ContainElements(
			map[string]any{"name": "THEOS_GO_REGISTRY", "value": "http://gitea.gitea.svc.cluster.local:3000/api/packages/" + ns + "/go"},
			map[string]any{"name": "GOPROXY", "value": "http://127.0.0.1:3127,direct"},
			map[string]any{"name": "GONOSUMDB", "value": "theos.local"},
		), "the agent fetches the owner's released Go modules from the Git host, through theos-agent's proxy only")

		By("opening a pull request for the pushed branch")
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.PullRequest).To(Equal(int32(1)))
			g.Expect(task.Status.Commit).To(Equal("sha-r1"))
			g.Expect(task.Status.Summary).To(Equal("Added crop."))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))

		By("reviewing once the checks pass")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "add-crop-r1-review-a1")
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskReviewing))
		}).Should(Succeed())

		By("sending it back when the reviewer asks for changes")
		finishSandbox(ns, "add-crop-r1-review-a1", "add-crop",
			&agentrunner.Result{Role: "review", Verdict: "changes", Summary: "Test an empty image."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(ContainSubstring("Test an empty image."))
			g.Expect(task.Status.Review).To(HaveField("Verdict", "changes"))
			sandboxExists(g, ns, "add-crop-r2-implement")
		}).Should(Succeed())
		Expect(gitFake.Reviews(ns, "orders-api", 1)).To(ConsistOf(And(HaveField("Event", ports.ReviewRequestChanges),
			HaveField("Commit", "sha-r1"), HaveField("Body", ContainSubstring("Test an empty image.")))),
			"the reviewer's verdict is a review in the Git host")

		By("merging the approved second round")
		agentEmail := model.AgentEmail(model.AgentUsername(ns, "orders-api"))
		gitFake.SetPullCommits(ns, "orders-api", 1,
			ports.PullCommit{SHA: "sha-r1", AuthorEmail: agentEmail, Parents: 1},
			ports.PullCommit{SHA: "sha-ada", AuthorEmail: "ada@example.com", Parents: 1},
			ports.PullCommit{SHA: "sha-main", AuthorEmail: "theos@example.com", Parents: 2},
			ports.PullCommit{SHA: "sha-r2", AuthorEmail: agentEmail, Parents: 1})
		gitFake.SetPullSize(ns, "orders-api", 1, 120, 30, 4)
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r2")
		finishSandbox(ns, "add-crop-r2-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r2", Summary: "Tested it."}, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r2", "success")
		Eventually(func(g Gomega) {
			review := sandboxExists(g, ns, "add-crop-r2-review-a1")
			containers, _, _ := unstructured.NestedSlice(review.Object, "spec", "podTemplate", "spec", "containers")
			g.Expect(containers[0].(map[string]any)["env"]).To(ContainElement(
				And(HaveKeyWithValue("name", "THEOS_FEEDBACK"), HaveKeyWithValue("value", ContainSubstring("Test an empty image.")))),
				"the reviewer checks the last round's feedback was addressed")
		}).Should(Succeed())
		finishSandbox(ns, "add-crop-r2-review-a1", "add-crop",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Complete and tested."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.CompletedAt).NotTo(BeNil())
			g.Expect(task.Status.Review).To(HaveField("Verdict", "approve"))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).NotTo(BeNil(), "%+v", task.Status.Conditions)
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
		expectSandboxGone(ns, "add-crop-r2-review-a1") // #1293

		By("keeping the merged pull request's size and its one person commit (measure-and-improve, decisions 3 and 4)")
		merged := getTask(Default, ns, "add-crop")
		Expect(merged.Status.PullRequestSize).To(Equal(c4v1beta1.TaskPullRequestSize{Additions: 120, Deletions: 30,
			Files: 4}))
		Expect(merged.Status.PersonCommits).To(Equal(new(int32(1))),
			"Ada's commit is a person's; the agent's and main's merge are not")

		By("serving the merged Task on the manager's metrics, as one a person wrote code for (#59)")
		Eventually(func(g Gomega) {
			g.Expect(factoryMetricValue(g, managerClient, model.MetricTasksMerged, map[string]string{
				"namespace": ns, "container": "orders-api", "kind": "Implement", "code": model.CodePerson})).To(Equal(1.0))
			g.Expect(factoryMetricValue(g, managerClient, model.MetricTasks, map[string]string{
				"namespace": ns, "container": "orders-api", "kind": "Implement", "phase": "Merged"})).To(Equal(1.0))
			g.Expect(factoryMetricValue(g, managerClient, model.MetricAgentRuns, map[string]string{
				"namespace": ns, "agent": "claude", "harness": "claude-code", "model": model.MetricNone, "role": "review"})).
				To(Equal(2.0), "round 1's review and round 2's")
		}).Should(Succeed())

		By("reading them again for a Task merged before they were kept")
		merged.Status.PullRequestSize, merged.Status.PersonCommits = c4v1beta1.TaskPullRequestSize{}, nil
		Expect(k8sClient.Status().Update(ctx, merged)).To(Succeed())
		gitFake.SetPullSize(ns, "orders-api", 1, 121, 30, 4)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.PullRequestSize).To(Equal(c4v1beta1.TaskPullRequestSize{Additions: 121, Deletions: 30,
				Files: 4}))
			g.Expect(task.Status.PersonCommits).To(Equal(new(int32(1))))
		}).Should(Succeed())

		By("keeping its timeline")
		var steps []string
		for _, tr := range getTask(Default, ns, "add-crop").Status.History {
			step := fmt.Sprintf("%s r%d", tr.Phase, tr.Round)
			if len(steps) == 0 || steps[len(steps)-1] != step {
				steps = append(steps, step)
			}
		}
		Expect(steps).To(Equal([]string{"Implementing r1", "Checking r1", "Reviewing r1",
			"Implementing r2", "Checking r2", "Reviewing r2", "Merged r2"}))
	})

	// mergedWithEdit runs a Task whose branch changes a file to its merge, and
	// returns the head it merged and the commit on main. moved, when set, is
	// the tree the head holds: not main's, as if main moved during the merge.
	mergedWithEdit := func(ns, moved string) (head string, merge ports.Commit) {
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		head, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"}, gitFake.MainHead(ns, "orders-api").Hash,
			"task/add-crop", scaffold.Tree{"crop.go": {Data: []byte("package crop\n")}}, nil, "", ports.Signature{})
		Expect(err).NotTo(HaveOccurred())
		if moved != "" {
			gitFake.SetCommitTree(ns, "orders-api", head, moved)
		}
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: head, Summary: "Added crop."}, "")
		gitFake.SetChecks(ns, "orders-api", head, "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, "add-crop-r1-review-a1", "add-crop",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Good."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
		return head, gitFake.MainHead(ns, "orders-api")
	}

	It("skips main's push run when the merge has the tree of the head that passed (#2060)", func() {
		ns := newProjectNamespace()
		head, merge := mergedWithEdit(ns, "")
		Expect(strings.SplitN(merge.Message, "\n", 2)[0]).To(HaveSuffix(" [skip ci]"), "the title carries the marker")
		Expect(gitFake.CommitStatusesWritten(ns, "orders-api", merge.Hash)).To(ConsistOf(ports.CommitStatus{
			Context: "check / check (pull_request)", State: ports.CheckSuccess,
			TargetURL:   "http://gitea/" + ns + "/orders-api/actions/runs/1",
			Description: "same tree as #1 head " + head,
		}), "main's head carries the head's result")
		checks, err := gitFake.CombinedStatus(ctx, ports.Repository{Owner: ns, Name: "orders-api"}, merge.Hash)
		Expect(err).NotTo(HaveOccurred())
		Expect(checks.State).To(Equal(ports.CheckSuccess))
	})

	It("marks a merge whose tree is not the head's as unchecked (#2060)", func() {
		ns := newProjectNamespace()
		_, merge := mergedWithEdit(ns, "tree-of-an-older-main")
		Expect(gitFake.CommitStatusesWritten(ns, "orders-api", merge.Hash)).To(ConsistOf(HaveField("State", ports.CheckError)))
		Expect(gitFake.CommitStatusesWritten(ns, "orders-api", merge.Hash)[0].Description).
			To(Equal("merged without a check: main moved during the merge"))
		checks, err := gitFake.CombinedStatus(ctx, ports.Repository{Owner: ns, Name: "orders-api"}, merge.Hash)
		Expect(err).NotTo(HaveOccurred())
		Expect(model.BaseState([]string{"check / check (pull_request)"}, checks.State, checks.Contexts)).
			To(Equal(ports.CheckFailure), "BaseRed shows it at once")
	})

	It("stops at its Crew's cost cap and goes on when the cap is raised (#1893)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 3)
		crew := newCrew(ns)
		crew.Spec.TaskBudgetUSD = 1
		task := newTask(ns, "add-crop", "Add crop")
		task.Labels = map[string]string{LabelCrew: crew.Name}
		mustCreate(crew, task)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", CostMilliUSD: new(int64(1000))}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}).Should(Succeed())

		By("starting no review once the runs' cost reached the cap")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			cond := condition(task.Status.Conditions, "Progressing")
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(model.ReasonOverBudget))
			g.Expect(cond.Message).To(Equal("The Task's runs cost $1.00, at its Crew's cap of $1; " +
				"raise the Crew's taskBudgetUSD to go on"))
		}).Should(Succeed())
		Consistently(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "add-crop-r1-review-a1"), newSandbox()))
		}).Should(BeTrue())

		By("starting the review once the cap is raised")
		Eventually(func(g Gomega) {
			var c c4v1beta1.Crew
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(crew), &c)).To(Succeed())
			c.Spec.TaskBudgetUSD = 2
			g.Expect(k8sClient.Update(ctx, &c)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }).Should(Succeed())
	})

	It("reviews under its Crew's cost cap (#1893)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 3)
		crew := newCrew(ns)
		crew.Spec.TaskBudgetUSD = 1
		task := newTask(ns, "add-crop", "Add crop")
		task.Labels = map[string]string{LabelCrew: crew.Name}
		mustCreate(crew, task)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", CostMilliUSD: new(int64(999))}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}).Should(Succeed())
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }).Should(Succeed())
	})

	It("has no cost cap when its Crew's taskBudgetUSD is 0, which omitempty leaves unset (#1893)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 3)
		crew := newCrew(ns)
		crew.Spec.TaskBudgetUSD = 0
		task := newTask(ns, "add-crop", "Add crop")
		task.Labels = map[string]string{LabelCrew: crew.Name}
		mustCreate(crew, task)
		var stored c4v1beta1.Crew
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(crew), &stored)).To(Succeed())
		Expect(stored.Spec.TaskBudgetUSD).To(BeZero())
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", CostMilliUSD: new(int64(250_000))}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}).Should(Succeed())

		By("starting the review however much the runs cost")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }).Should(Succeed())
		cond := condition(getTask(Default, ns, "add-crop").Status.Conditions, "Progressing")
		if cond != nil {
			Expect(cond.Reason).NotTo(Equal(model.ReasonOverBudget))
		}
	})

	It("adds up what its runs cost and used, and counts the runs whose agent didn't say", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", CostMilliUSD: new(int64(312)),
				Tokens: &model.TokenUsage{Input: 10, Output: 2000, CacheRead: 90000, CacheWrite: 30000, LargestSession: 140000}}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.CostMilliUSD).To(Equal(int64(312)))
			g.Expect(task.Status.RunsWithoutCost).To(BeZero())
			g.Expect(task.Status.Tokens).To(Equal(&c4v1beta1.TokenUsage{Input: 10, Output: 2000, CacheRead: 90000,
				CacheWrite: 30000, LargestSession: 140000}))
		}).Should(Succeed())

		By("changing the Agent's model while the checks run: the next runs are on it, the ended one keeps its own")
		var agent c4v1beta1.Agent
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: "claude"}, &agent)).To(Succeed())
		// A merge patch, not an Update of the read: the operator writes the
		// Agent too, and a stale resourceVersion would conflict (theos#27).
		before := agent.DeepCopy()
		agent.Spec.Model = "claude-sonnet-5-5"
		Expect(k8sClient.Patch(ctx, &agent, client.MergeFrom(before))).To(Succeed())
		// The review's sandbox records the Agent as the operator's cache has it
		// when it makes the sandbox. Passing the checks before the cache saw
		// the patch started the review on the Agent without a model, and the
		// spec then failed on main (#70): wait for the operator to see it.
		Eventually(func(g Gomega) {
			var seen c4v1beta1.Agent
			g.Expect(managerClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: "claude"}, &seen)).To(Succeed())
			g.Expect(seen.Spec.Model).To(Equal("claude-sonnet-5-5"))
		}).Should(Succeed())

		By("counting a review whose agent didn't report its cost")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, "add-crop-r1-review-a1", "add-crop",
			&agentrunner.Result{Role: "review", Verdict: "changes", Summary: "Test an empty image."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.CostMilliUSD).To(Equal(int64(312)))
			g.Expect(task.Status.RunsWithoutCost).To(Equal(int32(1)))
			g.Expect(task.Status.Tokens).To(Equal(&c4v1beta1.TokenUsage{Input: 10, Output: 2000, CacheRead: 90000,
				CacheWrite: 30000, LargestSession: 140000, RunsWithout: 1}))
		}).Should(Succeed())

		By("adding a smaller session's tokens, keeping the largest session, and counting its handoffs")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r2-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r2")
		finishSandbox(ns, "add-crop-r2-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r2", CostMilliUSD: new(int64(100)),
				Tokens:          &model.TokenUsage{Input: 5, Output: 1000, CacheRead: 40000, CacheWrite: 8000, LargestSession: 60000},
				SessionHandoffs: []int64{134000, 141200}}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.CostMilliUSD).To(Equal(int64(412)))
			g.Expect(task.Status.Tokens).To(Equal(&c4v1beta1.TokenUsage{Input: 15, Output: 3000, CacheRead: 130000,
				CacheWrite: 38000, LargestSession: 140000, RunsWithout: 1, SessionHandoffs: 2}))
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			var events eventsv1.EventList
			g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
			var notes []string
			for _, e := range events.Items {
				if e.Regarding.Kind == "Task" && e.Regarding.Name == "add-crop" && e.Reason == eventSessionHandedOff {
					g.Expect(e.Type).To(Equal(corev1.EventTypeNormal))
					notes = append(notes, e.Note)
				}
			}
			g.Expect(notes).To(Equal([]string{"Round 2's implement run handed off 2 times, at 134k and 141k tokens."}))
		}).Should(Succeed())

		By("recording each run: its round, role, Agent configuration, cost, tokens and handoff sizes")
		task := getTask(Default, ns, "add-crop")
		runs := task.Status.Runs
		for i := range runs {
			Expect(runs[i].EndedAt.IsZero()).To(BeFalse())
			runs[i].EndedAt = metav1.Time{}
		}
		const sonnet = "claude-sonnet-5-5"
		Expect(runs).To(Equal([]c4v1beta1.TaskAgentRun{
			{Round: 1, Role: "implement", Sandbox: "add-crop-r1-implement", Agent: "claude", Harness: "claude-code",
				CostMilliUSD: new(int64(312)),
				Tokens:       &c4v1beta1.TokenUsage{Input: 10, Output: 2000, CacheRead: 90000, CacheWrite: 30000, LargestSession: 140000}},
			{Round: 1, Role: "review", Sandbox: "add-crop-r1-review-a1", Agent: "claude", Harness: "claude-code",
				Model: sonnet, Tokens: &c4v1beta1.TokenUsage{RunsWithout: 1}},
			{Round: 2, Role: "implement", Sandbox: "add-crop-r2-implement", Agent: "claude", Harness: "claude-code",
				Model: sonnet, CostMilliUSD: new(int64(100)),
				Tokens: &c4v1beta1.TokenUsage{Input: 5, Output: 1000, CacheRead: 40000, CacheWrite: 8000,
					LargestSession: 60000, SessionHandoffs: 2},
				SessionHandoffs: []int64{134000, 141200}},
		}))
	})

	It("sends failed checks back to the implementer", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 2), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement", Commit: "sha-r1"}, "")
		gitFake.SetCheckLog("--- FAIL: TestA (0.00s)\n    a_test.go:9: got 2, want 1\n")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "failure")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(ContainSubstring("The CI checks failed"))
			g.Expect(task.Status.Feedback).To(ContainSubstring("What failed in CI:\n--- FAIL: TestA (0.00s)"))
		}).Should(Succeed())
	})

	It("goes on to CI when the check fails on main in the workspace too, saying so", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 2), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement", Commit: "sha-r1",
			CheckFailsOnBase: "download engine\ncurl: (22) 404 Not Found\n"}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "no round is spent")
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Phase", c4v1beta1.TaskChecking),
				HaveField("Round", int32(1)), HaveField("Message", ContainSubstring(
					"`make check` fails on main in the agent's workspace too, so CI decides")))),
				"the round's history keeps the step's message after the checks' wait replaces it")
		}).Should(Succeed())
		pr := getTask(Default, ns, "add-crop").Status.PullRequest
		Expect(gitFake.PullComments(ns, "orders-api", int(pr))).To(ContainElement(And(
			ContainSubstring("Round 1: `make check` fails in the agent's workspace on main too"),
			ContainSubstring("curl: (22) 404 Not Found"))))
		Eventually(func(g Gomega) {
			var events eventsv1.EventList
			g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
			var notes []string
			for _, e := range events.Items {
				if e.Regarding.Kind == "Task" && e.Regarding.Name == "add-crop" && e.Reason == eventCheckFailsOnBase {
					g.Expect(e.Type).To(Equal(corev1.EventTypeNormal))
					notes = append(notes, e.Note)
				}
			}
			g.Expect(notes).To(Equal([]string{"Round 1: `make check` fails on main in the agent's workspace too, " +
				"so CI decides (curl: (22) 404 Not Found)"}))
		}).Should(Succeed())
	})

	// #906: the notes a pull request gets as it opens are posted once, however
	// often the status write after them fails.
	It("says the check fails on main once when the status write after opening the pull request fails", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 2), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		taskKey := client.ObjectKey{Namespace: ns, Name: "add-crop"}
		taskStatusFaults.failNext(taskKey, 2, func(t *c4v1beta1.Task) bool {
			return t.Status.Phase == c4v1beta1.TaskChecking
		})
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement", Commit: "sha-r1",
			CheckFailsOnBase: "download engine\ncurl: (22) 404 Not Found\n"}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.PullRequest).NotTo(BeZero())
		}).Should(Succeed())
		Expect(taskStatusFaults.pending(taskKey)).To(BeZero())
		task := getTask(Default, ns, "add-crop")
		Expect(task.Status.Round).To(Equal(int32(1)))
		Expect(task.Status.Runs).To(HaveLen(1), "the retry keeps the run's result instead of implementing again")
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
		// The status is written before the note is posted, so the note may
		// follow it; it is posted once all the same.
		notes := func() int {
			return countContaining(gitFake.PullComments(ns, "orders-api", int(task.Status.PullRequest)),
				"`make check` fails in the agent's workspace on main too")
		}
		Eventually(notes).Should(Equal(1))
		Consistently(notes, "2s").Should(Equal(1))
		Consistently(func(g Gomega) {
			var events eventsv1.EventList
			g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
			var notes []string
			for _, e := range events.Items {
				if e.Regarding.Kind == "Task" && e.Regarding.Name == "add-crop" && e.Reason == eventCheckFailsOnBase {
					g.Expect(e.Series).To(BeNil(), "recorded once")
					notes = append(notes, e.Note)
				}
			}
			g.Expect(len(notes)).To(BeNumerically("<=", 1))
		}, "2s").Should(Succeed())
	})

	// #922: a review's verdict ends its run once, however often the status
	// write that records it fails: the retry finds the run, not a new review.
	It("keeps a review's verdict when the status write after it fails", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement", Commit: "sha-r1"}, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }).Should(Succeed())
		taskKey := client.ObjectKey{Namespace: ns, Name: "add-crop"}
		taskStatusFaults.failNext(taskKey, 2, func(t *c4v1beta1.Task) bool { return t.Status.Round == 2 })
		finishSandbox(ns, "add-crop-r1-review-a1", "add-crop", &agentrunner.Result{Role: "review", Verdict: "changes",
			Summary: "Test an empty image.", CostMilliUSD: new(int64(40))}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r2-implement") }).Should(Succeed())
		Expect(taskStatusFaults.pending(taskKey)).To(BeZero())
		task := getTask(Default, ns, "add-crop")
		Expect(task.Status.Round).To(Equal(int32(2)))
		Expect(task.Status.CostMilliUSD).To(Equal(int64(40)), "the review's cost is counted once")
		roles := make([]string, 0, len(task.Status.Runs))
		for _, run := range task.Status.Runs {
			roles = append(roles, fmt.Sprintf("r%d-%s", run.Round, run.Role))
		}
		Expect(roles).To(Equal([]string{"r1-implement", "r1-review"}), "no second review ran")
		expectSandboxGone(ns, "add-crop-r1-review-a1") // #1293
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "add-crop-r1-review-a2"), newSandbox()))).To(BeTrue())
	})

	It("fails a Task whose checks ran the runner out of room, costing no round", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 2), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement", Commit: "sha-r1"}, "")
		gitFake.SetCheckLog("compile: writing output: write $WORK/b001/_pkg_.a: no space left on device\n")
		DeferCleanup(gitFake.SetCheckLog, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "failure")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(
				HaveField("Reason", model.ReasonRunnerOutOfSpace))
			g.Expect(failedNoCI(&task.Status)).To(BeTrue(), "it costs its issue no try")
		}).Should(Succeed())
	})

	It("fails after the last round, saying why", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", nil, "OOMKilled")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			cond := condition(task.Status.Conditions, ConditionSucceeded)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(ContainSubstring("ended without a result: OOMKilled"))
			g.Expect(task.Status.RunsWithoutCost).To(Equal(int32(1)), "a run without a result reported no cost")
			g.Expect(task.Status.Tokens).To(Equal(&c4v1beta1.TokenUsage{RunsWithout: 1}), "nor tokens")
		}).Should(Succeed())
	})

	It("fails when its pull request is closed without merging", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 2), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }).Should(Succeed())
		gitFake.ClosePull(ns, "orders-api", 1)
		finishSandbox(ns, "add-crop-r1-review-a1", "add-crop",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Fine."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).
				To(HaveField("Reason", "PullRequestClosed"))
		}).Should(Succeed())
		Expect(gitFake.Reviews(ns, "orders-api", 1)).To(BeEmpty(), "no approval on a closed pull request")
	})

	It("refuses a Container whose default branch isn't protected", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), container, newAgent(ns, 1), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).
				To(HaveField("Message", ContainSubstring("has no Standard")))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "add-crop-r1-implement"), newSandbox()))).To(BeTrue())
	})

	It("waits for its Container, and for earlier Tasks on it", func() {
		ns := newProjectNamespace()
		mustCreate(newAgent(ns, 1),
			newTask(ns, "first", "First change"))
		Eventually(func(g Gomega) {
			g.Expect(condition(getTask(g, ns, "first").Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", "Container orders-api not found"))
		}).Should(Succeed())

		readyContainer(ns)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "first-r1-implement") }).Should(Succeed())
		mustCreate(newTask(ns, "second", "Second change"))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "second")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskPending))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", ContainSubstring("Waiting for Task first")))
		}).Should(Succeed())

		finishSandbox(ns, "first-r1-implement", "first", &agentrunner.Result{Role: "implement",
			Error: "the agent made no changes"}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "first").Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			sandboxExists(g, ns, "second-r1-implement")
		}).Should(Succeed())
	})

	It("waits for the Agent's credentials", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		agent := newAgent(ns, 1)
		agent.Spec.CredentialsSecretRef = &c4v1beta1.LocalRef{Name: "claude-credentials"}
		mustCreate(agent, newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) {
			cond := condition(getTask(g, ns, "add-crop").Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal("CredentialsNotFound"))
		}).Should(Succeed())
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "claude-credentials"),
			StringData: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": rand.Text()}})
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
	})

	It("gives an Agent on the shared token a sandbox with no token, which reaches the model proxy", func() {
		// #1136: the Task asks for the namespace's label, then for the source
		// Secret in the operator's namespace; the sandbox holds no Secret of its
		// own and goes to Claude through the proxy. The source starts missing;
		// the suite's other specs share it, so it is put back as it was.
		source := &corev1.Secret{}
		if err := k8sClient.Get(ctx, key(operatorNamespace, sharedToken), source); err == nil {
			Expect(k8sClient.Delete(ctx, source)).To(Succeed())
			DeferCleanup(func() {
				mustCreate(&corev1.Secret{ObjectMeta: objectMeta(operatorNamespace, sharedToken),
					Type: source.Type, Data: source.Data})
			})
		} else {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}
		ns := newProjectNamespace()
		readyContainer(ns)
		agent := newAgent(ns, 1)
		agent.Spec.CredentialsSecretRef = &c4v1beta1.LocalRef{Name: sharedToken}
		mustCreate(agent, newTask(ns, "add-crop", "Add crop"))
		waitsFor := func(message string) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				cond := condition(getTask(g, ns, "add-crop").Status.Conditions, ConditionProgressing)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Reason).To(Equal("CredentialsNotFound"))
				g.Expect(cond.Message).To(Equal(message))
			}).Should(Succeed())
		}

		By("asking for the label while the namespace hasn't opted in")
		waitsFor("Agent claude is on the factory's shared Secret " + sharedToken + ": label namespace " + ns + " " +
			c4v1beta1.LabelSharedSecrets + "=true, so its agents reach the factory's model proxy")

		By("naming the operator's namespace once it has, while the source is missing")
		namespace := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, key("", ns), namespace)).To(Succeed())
		Expect(patch(namespace, func() {
			namespace.Labels = map[string]string{c4v1beta1.LabelSharedSecrets: "true"}
		})).To(Succeed())
		waitsFor("Secret " + sharedToken + " of Agent claude not found in namespace " + operatorNamespace)

		By("starting the run once the source is there, with no token in the sandbox")
		mine := &corev1.Secret{ObjectMeta: objectMeta(operatorNamespace, sharedToken),
			StringData: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": rand.Text()}}
		Expect(k8sClient.Create(ctx, mine)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, mine))).To(Succeed()) })
		var sandbox *unstructured.Unstructured
		Eventually(func(g Gomega) { sandbox = sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		spec, _, _ := unstructured.NestedMap(sandbox.Object, "spec", "podTemplate", "spec")
		var pod corev1.PodSpec
		Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(spec, &pod)).To(Succeed())
		for _, v := range pod.Volumes {
			Expect(v.Secret).To(BeNil(), "volume %s", v.Name)
		}
		for _, c := range append(pod.Containers, pod.InitContainers...) {
			Expect(c.EnvFrom).To(BeEmpty(), "container %s", c.Name)
			for _, e := range c.Env {
				if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
					Expect(e.ValueFrom.SecretKeyRef.Name).NotTo(Equal(sharedToken), "env %s of container %s", e.Name, c.Name)
				}
			}
		}
		env := map[string]string{}
		for _, e := range pod.Containers[0].Env {
			env[e.Name] = e.Value
		}
		Expect(env).To(HaveKeyWithValue(envAnthropicBaseURL, testModelProxyURL))
		Expect(env).To(HaveKeyWithValue(envAnthropicAuthToken, modelProxyToken))
	})
})

var _ = Describe("Agent", func() {
	It("limits a session to 130000 tokens unless told, between 20000 and 1000000", func() {
		ns := newProjectNamespace()
		agent := newAgent(ns, 3)
		mustCreate(agent)
		Expect(agent.Spec.SessionTokenLimit).To(Equal(int32(130000)))

		for limit, err := range map[int32]string{19999: "greater than or equal to 20000",
			1000001: "less than or equal to 1000000"} {
			bad := newAgent(ns, 3)
			bad.Name, bad.Spec.SessionTokenLimit = fmt.Sprintf("limit-%d", limit), limit
			Expect(k8sClient.Create(ctx, bad)).To(MatchError(ContainSubstring(err)), "a limit of %d", limit)
		}
		for _, limit := range []int32{20000, 1000000} {
			good := newAgent(ns, 3)
			good.Name, good.Spec.SessionTokenLimit = fmt.Sprintf("limit-%d", limit), limit
			Expect(k8sClient.Create(ctx, good)).To(Succeed(), "a limit of %d", limit)
		}
	})
})

// playSuspension plays Agent Sandbox for a run the operator resumes in place:
// once the operator suspends the sandbox, its pod goes; then the operator
// resumes it, and the Task forgets the old pod (step 11 of
// docs/plans/self-hosting.md).
func playSuspension(ns, name string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		mode, _, _ := unstructured.NestedString(sandboxExists(g, ns, name).Object, "spec", "operatingMode")
		g.Expect(mode).To(Equal(operatingModeSuspended), "the operator suspends sandbox %s", name)
	}, "20s").Should(Succeed())
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: ns, Name: name}}, client.GracePeriodSeconds(0)))).To(Succeed())
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, name), &corev1.Pod{}))
	}).Should(BeTrue(), "the pod of sandbox %s is gone", name)
	Eventually(func(g Gomega) {
		mode, _, _ := unstructured.NestedString(sandboxExists(g, ns, name).Object, "spec", "operatingMode")
		g.Expect(mode).To(Equal(operatingModeRunning), "the operator resumes sandbox %s", name)
		g.Expect(getTask(g, ns, "add-crop").Status.SandboxPod).To(BeEmpty(), "the Task forgets the old pod")
	}, "20s").Should(Succeed())
}

// sandboxPodSpec is the pod template's spec of a sandbox.
func sandboxPodSpec(g Gomega, sandbox *unstructured.Unstructured) corev1.PodSpec {
	spec, _, err := unstructured.NestedMap(sandbox.Object, "spec", "podTemplate", "spec")
	g.Expect(err).NotTo(HaveOccurred())
	var pod corev1.PodSpec
	g.Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(spec, &pod)).To(Succeed())
	return pod
}

var _ = Describe("Task with an interrupted run", func() {
	It("resumes it in place up to MaxResumes times, then reruns it up to MaxReruns times, then spends the round", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		killed := &agentrunner.Result{Role: "implement", Error: "claude failed: signal: killed", Interrupted: true}
		first := model.SandboxName("add-crop", 1, 0, model.RoleImplementer, 1, 0)
		Eventually(func(g Gomega) { sandboxExists(g, ns, first) }, "20s").Should(Succeed())
		for resume := 1; resume <= model.MaxResumes; resume++ {
			finishSandbox(ns, first, "add-crop", killed, "")
			playSuspension(ns, first)
			Eventually(func(g Gomega) {
				task := getTask(g, ns, "add-crop")
				g.Expect(task.Status.Resumes).To(Equal(int32(resume)))
				g.Expect(task.Status.Reruns).To(BeZero(), "a resume costs no rerun")
				g.Expect(task.Status.Round).To(Equal(int32(1)), "an interrupted run costs no round")
				g.Expect(task.Status.Sandbox).To(Equal(first), "the run resumes in its own sandbox")
			}).Should(Succeed())
		}

		By("rerunning past MaxResumes")
		for rerun := 1; rerun <= model.MaxReruns+1; rerun++ {
			// The sixth interruption reruns in a sandbox of its own: add-crop-r1-implement-x1, then -x2.
			name := model.SandboxName("add-crop", 1, 0, model.RoleImplementer, 1, rerun-1)
			Eventually(func(g Gomega) { sandboxExists(g, ns, name) }, "20s").Should(Succeed())
			finishSandbox(ns, name, "add-crop", killed, "")
			if rerun > model.MaxReruns {
				break
			}
			Eventually(func(g Gomega) {
				task := getTask(g, ns, "add-crop")
				g.Expect(task.Status.Reruns).To(Equal(int32(rerun)))
				g.Expect(task.Status.Resumes).To(Equal(int32(model.MaxResumes)))
				g.Expect(task.Status.Round).To(Equal(int32(1)), "an interrupted run costs no round")
			}).Should(Succeed())
		}

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)), "past the caps the round is spent")
			g.Expect(task.Status.Reruns).To(BeZero())
			g.Expect(task.Status.Resumes).To(BeZero(), "each round resumes afresh")
		}).Should(Succeed())
	})

	It("gives the sandbox a checkpoint volume, and resumes an interrupted run with the current Agent and its remaining time", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		const name = "add-crop-r1-implement"
		var sandbox *unstructured.Unstructured
		Eventually(func(g Gomega) { sandbox = sandboxExists(g, ns, name) }, "20s").Should(Succeed())

		By("making the pod with the checkpoint volume and a minute to save a checkpoint")
		claims, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "volumeClaimTemplates")
		Expect(claims).To(ConsistOf(And(
			HaveKeyWithValue("metadata", HaveKeyWithValue("name", checkpointVolume)),
			HaveKeyWithValue("spec", HaveKeyWithValue("resources",
				HaveKeyWithValue("requests", HaveKeyWithValue("storage", "1Gi")))))))
		pod := sandboxPodSpec(Default, sandbox)
		Expect(pod.TerminationGracePeriodSeconds).To(HaveValue(Equal(int64(60))))
		Expect(pod.Containers[0].VolumeMounts).To(ContainElement(
			corev1.VolumeMount{Name: checkpointVolume, MountPath: agentrunner.CheckpointDir}))
		Expect(pod.Containers[0].Env).To(ContainElement(
			corev1.EnvVar{Name: agentrunner.EnvCheckpointDir, Value: agentrunner.CheckpointDir}))
		shutdownBefore, _, _ := unstructured.NestedString(sandbox.Object, "spec", "shutdownTime")

		By("suspending the Sandbox when a node shutdown ends its pod after nine minutes")
		started := time.Now().Truncate(time.Second).Add(-9 * time.Minute)
		running := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
			Labels:          map[string]string{LabelTask: "add-crop"},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent"}}}}
		Expect(k8sClient.Create(ctx, running)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.SandboxPod).To(Equal(string(running.UID)))
		}).Should(Succeed())
		running.Status = corev1.PodStatus{Phase: corev1.PodFailed, StartTime: &metav1.Time{Time: started},
			Conditions: []corev1.PodCondition{{Type: corev1.DisruptionTarget, Status: corev1.ConditionTrue,
				Reason: "TerminationByKubelet", LastTransitionTime: metav1.Now()}},
			ContainerStatuses: []corev1.ContainerStatus{{Name: "agent", Image: "agent", State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 143,
					StartedAt:  metav1.Time{Time: started},
					FinishedAt: metav1.Time{Time: started.Add(9 * time.Minute)}}}}}}
		Expect(k8sClient.Status().Update(ctx, running)).To(Succeed())
		Eventually(func(g Gomega) {
			sandbox = sandboxExists(g, ns, name)
			mode, _, _ := unstructured.NestedString(sandbox.Object, "spec", "operatingMode")
			g.Expect(mode).To(Equal(operatingModeSuspended))
			g.Expect(sandbox.GetAnnotations()).To(HaveKeyWithValue(annotationRanSeconds, "540"))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Resumes).To(Equal(int32(1)))
			g.Expect(task.Status.Reruns).To(BeZero())
			g.Expect(task.Status.SandboxPod).To(Equal(string(running.UID)), "the old pod is the run's until it goes")
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "Interrupted"),
				HaveField("Message", ContainSubstring("resumes in place from its checkpoint (resume 1 of 5)")))))
		}, "20s").Should(Succeed())
		Consistently(func(g Gomega) {
			mode, _, _ := unstructured.NestedString(sandboxExists(g, ns, name).Object, "spec", "operatingMode")
			g.Expect(mode).To(Equal(operatingModeSuspended), "the sandbox stays suspended while its pod is there")
		}, "2s").Should(Succeed())

		By("resuming it with the Agent's new image once the pod is gone")
		Eventually(func(g Gomega) {
			var agent c4v1beta1.Agent
			g.Expect(k8sClient.Get(ctx, key(ns, "claude"), &agent)).To(Succeed())
			agent.Spec.Image = "theos/agent-go:new"
			g.Expect(k8sClient.Update(ctx, &agent)).To(Succeed())
		}).Should(Succeed())
		Expect(k8sClient.Delete(ctx, running, client.GracePeriodSeconds(0))).To(Succeed())
		var agent c4v1beta1.Agent
		Expect(k8sClient.Get(ctx, key(ns, "claude"), &agent)).To(Succeed())
		remaining := int64((runTimeout(&agent) - 9*time.Minute).Seconds())
		Eventually(func(g Gomega) {
			sandbox = sandboxExists(g, ns, name)
			mode, _, _ := unstructured.NestedString(sandbox.Object, "spec", "operatingMode")
			g.Expect(mode).To(Equal(operatingModeRunning))
			pod := sandboxPodSpec(g, sandbox)
			g.Expect(pod.ActiveDeadlineSeconds).To(HaveValue(Equal(remaining)), "the run's remaining time")
			g.Expect(pod.Containers[0].Image).To(Equal("theos/agent-go:new"), "the current Agent's image")
			g.Expect(pod.TerminationGracePeriodSeconds).To(HaveValue(Equal(int64(60))))
			shutdown, _, _ := unstructured.NestedString(sandbox.Object, "spec", "shutdownTime")
			g.Expect(parseTime(shutdown)).To(BeTemporally(">", time.Now().Add(time.Duration(remaining)*time.Second)),
				"a shutdown time past the new deadline")
			g.Expect(shutdown).NotTo(Equal(shutdownBefore))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.SandboxPod).To(BeEmpty(), "the Task forgets the old pod")
			g.Expect(task.Status.Sandbox).To(Equal(name))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "Running"),
				HaveField("Message", ContainSubstring("Resuming the interrupted run of round 1 in place")))))
		}, "20s").Should(Succeed())

		By("going on with the resumed run's pod as the run's")
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, name, "add-crop", &agentrunner.Result{Role: "implement", Commit: "sha-r1",
			Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}, "20s").Should(Succeed())
	})

	It("resumes in place a round whose Git host stayed unreachable, without counting a rerun", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }, "20s").Should(Succeed())
		// Gitea restarting through every retry of the clone, as theos-agent reports it.
		unreachable := &agentrunner.Result{Role: "implement", Interrupted: true, Error: "the Git host stayed " +
			"unreachable: git clone: exit status 128: fatal: unable to access 'http://gitea/apps/x.git/': " +
			"Couldn't connect to server"}
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", unreachable, "")
		playSuspension(ns, "add-crop-r1-implement")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Resumes).To(Equal(int32(1)))
			g.Expect(task.Status.Reruns).To(BeZero())
			g.Expect(task.Status.Round).To(Equal(int32(1)), "an unreachable Git host costs no round")
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "Interrupted"),
				HaveField("Message", ContainSubstring("the Git host stayed unreachable")))))
		}, "20s").Should(Succeed())
	})

	It("resumes a round whose pod was deleted and replaced under the same sandbox", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())

		// Agent Sandbox starts the pod of the round's sandbox; the operator
		// remembers it. A pod that goes before it finished, replaced or not,
		// is an interruption the run resumes from in place, in its sandbox.
		startPod := func() *corev1.Pod {
			GinkgoHelper()
			sandbox := newSandbox()
			Eventually(func(g Gomega) {
				name := getTask(g, ns, "add-crop").Status.Sandbox
				g.Expect(name).NotTo(BeEmpty(), "the Task names its run's sandbox")
				g.Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed())
				g.Expect(sandbox.GetDeletionTimestamp().IsZero()).To(BeTrue(), "the sandbox %s is live", name)
			}).Should(Succeed())
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: sandbox.GetName(),
					Labels:          map[string]string{LabelTask: "add-crop"},
					OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent"}}},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			return pod
		}
		first := startPod()
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.SandboxPod).To(Equal(string(first.UID)))
		}).Should(Succeed())

		By("force-deleting the pod, which Agent Sandbox replaces")
		Expect(k8sClient.Delete(ctx, first, client.GracePeriodSeconds(0))).To(Succeed())
		startPod()
		playSuspension(ns, "add-crop-r1-implement")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Resumes).To(Equal(int32(1)))
			g.Expect(task.Status.Reruns).To(BeZero())
			g.Expect(task.Status.Round).To(Equal(int32(1)), "an interrupted run costs no round")
			g.Expect(task.Status.Sandbox).To(Equal("add-crop-r1-implement"))
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "Interrupted"),
				HaveField("Message", ContainSubstring("(resume 1 of 5)")))))
		}, "20s").Should(Succeed())
	})

	It("reruns a review lost in a cluster outage, but spends one that ran out its time", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }, "20s").Should(Succeed())

		By("rerunning the review whose pod an outage past its shutdown time took")
		expireSandbox(ns, "add-crop-r1-review-a1", 7*time.Hour)
		rerun := model.SandboxName("add-crop", 1, 0, model.RoleReviewer, 1, 1)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, rerun)
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Reruns).To(Equal(int32(1)))
			g.Expect(task.Status.ReviewAttempts).To(BeZero(), "an outage costs no review attempt")
		}, "20s").Should(Succeed())

		By("spending an attempt on a review that ran out its time")
		expireSandbox(ns, rerun, 0)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, model.SandboxName("add-crop", 1, 0, model.RoleReviewer, 2, 1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.ReviewAttempts).To(Equal(int32(1)))
			g.Expect(task.Status.Reruns).To(Equal(int32(1)))
		}, "20s").Should(Succeed())
	})
})

var _ = Describe("Task with an unavailable agent", func() {
	It("keeps its round and tries the same step again later", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		back := time.Now().Add(3 * time.Second).UTC()
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement",
			Error: "claude ended with an error: usage limit reached", Unavailable: true, RetryAt: &back}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "a usage limit costs no round")
			g.Expect(task.Status.AgentBackAt).NotTo(BeNil())
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(HaveField("Reason", "AgentUnavailable"))
		}).Should(Succeed())
		// Agent Sandbox would collect the old pod with its sandbox.
		Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: objectMeta(ns, "add-crop-r1-implement")})).To(Succeed())

		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "add-crop-r1-implement")
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.AgentBackAt).To(BeNil())
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}, "20s").Should(Succeed())
	})

	// Step 13 of docs/plans/self-hosting.md (#1996).
	It("suspends a run that saved its checkpoint until RetryAt, then resumes it in place with the current "+
		"Agent, no round spent (#1996)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		const name = "add-crop-r1-implement"
		runningPod(ns, name, "add-crop")
		back := time.Now().Add(8 * time.Second).Truncate(time.Second).UTC()
		finishSandbox(ns, name, "add-crop", &agentrunner.Result{Role: "implement",
			Error: "claude ended with an error: Claude AI usage limit reached", Unavailable: true, RetryAt: &back,
			Checkpoint: agentrunner.CheckpointClean, CostMilliUSD: new(int64(250))}, "")

		By("suspending the Sandbox, its volume kept, the run's cost counted and no round, rerun or resume spent")
		Eventually(func(g Gomega) {
			sandbox := sandboxExists(g, ns, name)
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeSuspended))
			g.Expect(sandbox.GetAnnotations()).To(HaveKeyWithValue(annotationAgentBack, back.Format(time.RFC3339)))
			_, hasShutdown, _ := unstructured.NestedString(sandbox.Object, "spec", "shutdownTime")
			g.Expect(hasShutdown).To(BeFalse(), "the wait may last past the sandbox's shutdown time")
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "a usage limit costs no round")
			g.Expect(task.Status.Resumes).To(BeZero(), "a wait for the agent is no interruption")
			g.Expect(task.Status.Reruns).To(BeZero())
			g.Expect(task.Status.CostMilliUSD).To(Equal(int64(250)))
			g.Expect(task.Status.AgentBackAt).NotTo(BeNil())
			g.Expect(task.Status.AgentBackAt.Time).To(BeTemporally("==", back))
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "AgentUnavailable"),
				HaveField("Message", ContainSubstring("the run of round 1 saved its checkpoint and resumes in "+
					"place when the agent is back")))))
		}, "20s").Should(Succeed())

		By("waiting, suspended, for the agent once the pod went")
		deletePod(ns, name)
		Eventually(func(g Gomega) {
			cond := condition(getTask(g, ns, "add-crop").Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal("AgentUnavailable"))
			g.Expect(cond.Message).To(ContainSubstring("the run of round 1 resumes in place in"))
		}, "20s").Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeSuspended))
		}, "2s").Should(Succeed())
		Eventually(func(g Gomega) {
			var agent c4v1beta1.Agent
			g.Expect(k8sClient.Get(ctx, key(ns, "claude"), &agent)).To(Succeed())
			agent.Spec.Image = "theos/agent-go:new"
			g.Expect(k8sClient.Update(ctx, &agent)).To(Succeed())
		}).Should(Succeed())

		By("resuming it in place at RetryAt with the current Agent")
		Eventually(func(g Gomega) {
			sandbox := sandboxExists(g, ns, name)
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeRunning))
			g.Expect(sandbox.GetAnnotations()).NotTo(HaveKey(annotationAgentBack))
			g.Expect(sandboxPodSpec(g, sandbox).Containers[0].Image).To(Equal("theos/agent-go:new"))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Sandbox).To(Equal(name))
			g.Expect(task.Status.SandboxPod).To(BeEmpty(), "the Task forgets the old pod")
			g.Expect(task.Status.AgentBackAt).To(BeNil())
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Resumes).To(BeZero(), "the resume counts against no cap")
			g.Expect(task.Status.Reruns).To(BeZero())
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "Running"),
				HaveField("Message", ContainSubstring("Resuming the run of round 1 in place: its agent is due back")))))
		}, "20s").Should(Succeed())
		Expect(time.Now()).To(BeTemporally(">=", back), "not before the agent is back")
	})

	It("handles one in a Sandbox without the checkpoint volume as before (#1996)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		const name = "add-crop-r1-implement"
		By("a Sandbox made before checkpoints, with no checkpoint claim")
		old := newSandbox()
		old.SetNamespace(ns)
		old.SetName(name)
		old.SetLabels(map[string]string{LabelTask: "add-crop"})
		Expect(unstructured.SetNestedSlice(old.Object, []any{map[string]any{"name": "agent", "image": "agent"}},
			"spec", "podTemplate", "spec", "containers")).To(Succeed())
		Expect(k8sClient.Create(ctx, old)).To(Succeed())
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Sandbox).To(Equal(name))
		}, "20s").Should(Succeed())

		back := time.Now().Add(3 * time.Second).UTC()
		finishSandbox(ns, name, "add-crop", &agentrunner.Result{Role: "implement",
			Error: "claude ended with an error: usage limit reached", Unavailable: true, RetryAt: &back,
			Checkpoint: agentrunner.CheckpointClean}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(1)), "a usage limit costs no round")
			g.Expect(task.Status.Resumes).To(BeZero())
			g.Expect(task.Status.AgentBackAt).NotTo(BeNil())
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "AgentUnavailable"),
				HaveField("Message", ContainSubstring("round 1 waits for it")))))
			sandbox := newSandbox()
			err := k8sClient.Get(ctx, key(ns, name), sandbox)
			g.Expect(err == nil && sandbox.GetUID() == old.GetUID()).To(BeFalse(), "the old sandbox is not kept")
		}, "20s").Should(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: objectMeta(ns, name)}))).
			To(Succeed())

		By("starting the step again in a new sandbox once the agent is back")
		Eventually(func(g Gomega) {
			sandbox := sandboxExists(g, ns, name)
			g.Expect(sandbox.GetUID()).NotTo(Equal(old.GetUID()))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.AgentBackAt).To(BeNil())
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}, "20s").Should(Succeed())
	})
})

var _ = Describe("Agent tokens after an upgrade", func() {
	It("revokes the bot's old token once no sandbox of the Container uses it, and not before", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		user := ns + "-orders-api-agent"

		By("an operator before the upgrade made a token with fewer scopes, which a running sandbox holds")
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "orders-api-agent-git"), Data: map[string][]byte{
			"username": []byte(user), "token": []byte("old"), "scopes": []byte("write:repository")}})
		running := newSandbox()
		running.SetNamespace(ns)
		running.SetName("fix-login-r1-implement")
		running.SetLabels(map[string]string{c4v1beta1.LabelContainer: "orders-api", LabelAgentSandbox: agentSandboxValue})
		Expect(unstructured.SetNestedSlice(running.Object, []any{map[string]any{"name": "agent", "image": "agent"}},
			"spec", "podTemplate", "spec", "containers")).To(Succeed())
		Expect(k8sClient.Create(ctx, running)).To(Succeed())

		By("making a new token for the next run, beside the old one")
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-agent-git"), &secret)).To(Succeed())
		Expect(string(secret.Data["scopes"])).To(Equal(ports.AgentTokenScopes))
		Expect(secret.Annotations).To(HaveKey(annotationOldTokensBefore))

		By("keeping the old token while the sandbox started before it runs")
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}).Should(Succeed())
		Expect(gitFake.Revocations(user)).To(BeZero(), "a running agent never loses its token")
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-agent-git"), &secret)).To(Succeed())
		Expect(secret.Annotations).To(HaveKey(annotationOldTokensBefore))

		By("revoking it at the next run once that sandbox is gone")
		Expect(k8sClient.Delete(ctx, running)).To(Succeed())
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }, "20s").Should(Succeed())
		Expect(gitFake.Revocations(user)).To(Equal(1))
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-agent-git"), &secret)).To(Succeed())
		Expect(secret.Annotations).NotTo(HaveKey(annotationOldTokensBefore))
	})
})
