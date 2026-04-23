package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	forgev1 "github.com/forgeplatform/forge-operator/api/v1alpha1"
)

func TestScheduleLifecycle(t *testing.T) {
	mock := newMockForge()
	srv, _ := mock.start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := newManager(t, ctx, srv.URL, "test-token")
	defer stop()

	// Pre-seed a JobTemplate in the mock (Schedule controller resolves
	// JT by name, not via the JobTemplate CRD).
	mock.mu.Lock()
	mock.jobTemplates[42] = map[string]any{
		"id":        int64(42),
		"name":      "deploy-web",
		"inventory": int64(2),
		"project":   int64(1),
		"playbook":  "site.yml",
	}
	mock.mu.Unlock()

	cr := &forgev1.Schedule{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "default"},
		Spec: forgev1.ScheduleSpec{
			JobTemplate: "deploy-web",
			RRule:       "DTSTART:20260101T020000Z RRULE:FREQ=WEEKLY;INTERVAL=1;BYDAY=MO",
			ExtraData:   "k: v",
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	if !pollUntil(t, 15*time.Second, func() bool {
		var got forgev1.Schedule
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "nightly", Namespace: "default"}, &got); err != nil {
			return false
		}
		return got.Status.ForgeID > 0
	}) {
		t.Fatal("timeout: schedule ForgeID not set")
	}

	if mock.CallCount("POST schedules") != 1 {
		t.Errorf("expected 1 POST schedules, got %d", mock.CallCount("POST schedules"))
	}

	// next_run should be propagated.
	var got forgev1.Schedule
	_ = k8sClient.Get(ctx, types.NamespacedName{Name: "nightly", Namespace: "default"}, &got)
	if got.Status.NextRun != "2026-04-30T02:00:00Z" {
		t.Errorf("nextRun not propagated: got %q", got.Status.NextRun)
	}
	if got.Status.JobTemplateID != 42 {
		t.Errorf("jobTemplateId want 42 got %d", got.Status.JobTemplateID)
	}

	// Update RRule and verify PATCH.
	got.Spec.RRule = "DTSTART:20260101T020000Z RRULE:FREQ=WEEKLY;INTERVAL=1;BYDAY=TU"
	if err := k8sClient.Update(ctx, &got); err != nil {
		t.Fatalf("update CR: %v", err)
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("PATCH schedule") >= 1
	}) {
		t.Fatal("timeout: PATCH schedule never called")
	}

	// Delete.
	if err := k8sClient.Delete(ctx, &got); err != nil {
		t.Fatalf("delete CR: %v", err)
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("DELETE schedule") >= 1
	}) {
		t.Fatal("timeout: DELETE schedule never called")
	}
}
