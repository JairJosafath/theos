package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// #591: a status written between the Task's read and its adoption stays. The
// adoption's patch reads the Task back, and with it a newer resourceVersion,
// so a status computed from the first read would pass the optimistic lock and
// overwrite the newer one.
func TestAdoptingATaskKeepsAStatusWrittenSinceTheRead(t *testing.T) {
	scheme := briefScheme(t)
	task := &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "merged-one"), Spec: c4v1beta1.TaskSpec{
		ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"},
		AgentRef:     c4v1beta1.LocalRef{Name: "implementer"}, // missing: a step would write AgentNotFound
		Goal:         "Add crop",
	}}
	container := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api")}
	merged := false
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Task{}).
		WithObjects(task, container).
		WithInterceptorFuncs(interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch,
			obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*c4v1beta1.Task); ok && !merged {
				merged = true
				var current c4v1beta1.Task
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), &current); err != nil {
					return err
				}
				current.Status.Phase = c4v1beta1.TaskMerged
				current.Status.PhaseChangedAt = &metav1.Time{Time: time.Now()}
				if err := c.Status().Update(ctx, &current); err != nil {
					return err
				}
			}
			return c.Patch(ctx, obj, patch, opts...)
		}}).Build()
	r := &TaskReconciler{Client: c, APIReader: c, Scheme: scheme}
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(task)}); err != nil {
		t.Fatal(err)
	}
	if !merged {
		t.Fatal("the reconcile didn't adopt the Task")
	}
	var got c4v1beta1.Task
	if err := c.Get(ctx, client.ObjectKeyFromObject(task), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != c4v1beta1.TaskMerged {
		t.Errorf("phase is %q, want %q: the reconcile overwrote the newer status", got.Status.Phase,
			c4v1beta1.TaskMerged)
	}
}
