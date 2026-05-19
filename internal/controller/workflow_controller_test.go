package controller

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	forgev1 "github.com/forgeplatform/forge-operator/api/v1alpha1"
)

func TestWorkflowLifecycle(t *testing.T) {
	mock := newMockForge()
	srv, _ := mock.start(t)

	// Seed two JobTemplates the workflow nodes will reference.
	mock.mu.Lock()
	mock.jobTemplates[10] = map[string]any{"id": int64(10), "name": "Provision EC2"}
	mock.jobTemplates[11] = map[string]any{"id": int64(11), "name": "Deploy App"}
	mock.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := newManager(t, ctx, srv.URL, "test-token")
	defer stop()

	cr := &forgev1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "build-and-deploy", Namespace: "default"},
		Spec: forgev1.WorkflowSpec{
			Description:  "Build then deploy",
			Organization: "Default",
			Nodes: []forgev1.WorkflowNode{
				{
					Identifier:         "build",
					UnifiedJobTemplate: "Provision EC2",
					SuccessNodes:       []string{"deploy"},
				},
				{
					Identifier:         "deploy",
					UnifiedJobTemplate: "Deploy App",
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	if !pollUntil(t, 10*time.Second, func() bool {
		var got forgev1.Workflow
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "build-and-deploy", Namespace: "default"}, &got); err != nil {
			return false
		}
		return got.Status.ForgeID > 0 && got.Status.NodeCount == 2
	}) {
		t.Fatal("timeout: workflow not fully reconciled")
	}
	if mock.CallCount("POST workflows") < 1 {
		t.Fatal("expected POST workflows")
	}
	if mock.CallCount("POST workflow node") < 2 {
		t.Fatalf("expected 2+ POST workflow node, got %d", mock.CallCount("POST workflow node"))
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("ASSOCIATE workflow edge") >= 1
	}) {
		t.Fatal("timeout: ASSOCIATE workflow edge never called (build->deploy)")
	}

	// Delete: cascade-delete cleans nodes too.
	if err := k8sClient.Delete(ctx, cr); err != nil {
		t.Fatalf("delete CR: %v", err)
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("DELETE workflow") >= 1
	}) {
		t.Fatal("timeout: DELETE workflow never called")
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		var x forgev1.Workflow
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "build-and-deploy", Namespace: "default"}, &x)
		return apierrors.IsNotFound(err)
	}) {
		t.Fatal("timeout: CR not removed after finalizer")
	}
}
