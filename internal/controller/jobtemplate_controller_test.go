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

func TestJobTemplateLifecycle(t *testing.T) {
	mock := newMockForge()
	srv, _ := mock.start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := newManager(t, ctx, srv.URL, "test-token")
	defer stop()

	// Seed an inventory we can reference (controller doesn't need to
	// create it — it's just an ID lookup).
	mock.mu.Lock()
	mock.inventories[2] = map[string]any{"id": int64(2), "name": "Demo Inventory", "organization": int64(1)}
	mock.mu.Unlock()

	// --- Create CR ---
	cr := &forgev1.JobTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "deploy-web", Namespace: "default"},
		Spec: forgev1.JobTemplateSpec{
			Description: "Test deploy",
			Inventory:   "Demo Inventory",
			Project:     "Demo Project",
			Playbook:    "site.yml",
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// Wait for reconcile to POST job_templates and write status.
	if !pollUntil(t, 10*time.Second, func() bool {
		var got forgev1.JobTemplate
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "deploy-web", Namespace: "default"}, &got); err != nil {
			return false
		}
		return got.Status.ForgeID > 0
	}) {
		t.Fatal("timeout: forgeId not set")
	}

	if mock.CallCount("POST job_templates") < 1 {
		t.Fatalf("expected POST job_templates, got %d", mock.CallCount("POST job_templates"))
	}

	// --- Update CR (drift) ---
	var got forgev1.JobTemplate
	_ = k8sClient.Get(ctx, types.NamespacedName{Name: "deploy-web", Namespace: "default"}, &got)
	got.Spec.Description = "Updated description"
	if err := k8sClient.Update(ctx, &got); err != nil {
		t.Fatalf("update CR: %v", err)
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("PATCH jobtemplate") >= 1
	}) {
		t.Fatal("timeout: PATCH jobtemplate never called")
	}

	// --- Delete CR (finalizer should DELETE in Forge) ---
	if err := k8sClient.Delete(ctx, &got); err != nil {
		t.Fatalf("delete CR: %v", err)
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("DELETE jobtemplate") >= 1
	}) {
		t.Fatal("timeout: DELETE jobtemplate never called")
	}
	// CR should be gone after finalizer runs.
	if !pollUntil(t, 10*time.Second, func() bool {
		var x forgev1.JobTemplate
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "deploy-web", Namespace: "default"}, &x)
		return apierrors.IsNotFound(err)
	}) {
		t.Fatal("timeout: CR not removed after finalizer")
	}

	// Mock state: jobTemplate gone.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.jobTemplates) != 0 {
		t.Fatalf("expected 0 jobTemplates in mock, got %d", len(mock.jobTemplates))
	}
}
