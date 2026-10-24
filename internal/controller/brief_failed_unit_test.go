package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A Task that failed Blocked lists its issue as kind blocked (#492).
func TestIssueTasksMarksBlocked(t *testing.T) {
	task := func(name string, phase c4v1beta1.TaskPhase, reason string) c4v1beta1.Task {
		tk := c4v1beta1.Task{ObjectMeta: metav1.ObjectMeta{Name: name}}
		tk.Spec.Issue, tk.Spec.ContainerRef.Name = 1, "cli"
		tk.Status.Phase = phase
		tk.Status.Conditions = []metav1.Condition{{Type: ConditionSucceeded, Status: metav1.ConditionFalse,
			Reason: reason, Message: "msg"}}
		return tk
	}
	got := issueTasks([]c4v1beta1.Task{
		task("blocked", c4v1beta1.TaskFailed, model.ReasonBlocked),
		task("failed", c4v1beta1.TaskFailed, "Failed"),
		task("waiting", c4v1beta1.TaskImplementing, model.ReasonBlocked),
	})["cli"]
	want := map[string]bool{"blocked": true, "failed": false, "waiting": false}
	if len(got) != len(want) {
		t.Fatalf("issueTasks() = %+v", got)
	}
	for _, it := range got {
		if it.Blocked != want[it.Name] {
			t.Errorf("Task %s: Blocked = %v, want %v", it.Name, it.Blocked, want[it.Name])
		}
	}
}
