package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	forgev1 "github.com/forgeplatform/forge-operator/api/v1alpha1"
)

func TestInventoryLifecycle(t *testing.T) {
	mock := newMockForge()
	srv, _ := mock.start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := newManager(t, ctx, srv.URL, "test-token")
	defer stop()

	cr := &forgev1.Inventory{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-inv", Namespace: "default"},
		Spec: forgev1.InventorySpec{
			Description:  "test inventory",
			Organization: "Default",
			Variables:    "k: v\n",
			Hosts: []forgev1.InventoryHost{
				{Name: "web-1", Enabled: true},
				{Name: "web-2", Enabled: true},
				{Name: "db-1", Enabled: true},
			},
			Groups: []forgev1.InventoryGroup{
				{Name: "web", Hosts: []string{"web-1", "web-2"}},
				{Name: "db", Hosts: []string{"db-1"}},
				{Name: "all-prod", Children: []string{"web", "db"}},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	if !pollUntil(t, 15*time.Second, func() bool {
		var got forgev1.Inventory
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "prod-inv", Namespace: "default"}, &got); err != nil {
			return false
		}
		return got.Status.ForgeID > 0
	}) {
		t.Fatal("timeout: inventory ForgeID not set")
	}

	mock.mu.Lock()
	if len(mock.inventories) != 1 {
		t.Errorf("expected 1 inventory, got %d", len(mock.inventories))
	}
	if len(mock.hosts) != 3 {
		t.Errorf("expected 3 hosts, got %d", len(mock.hosts))
	}
	if len(mock.groups) != 3 {
		t.Errorf("expected 3 groups, got %d", len(mock.groups))
	}
	// Each non-empty group membership needs at least one ASSOCIATE call.
	mock.mu.Unlock()

	if mock.CallCount("ASSOCIATE group host") < 3 {
		t.Errorf("expected >=3 host associations (web-1, web-2, db-1), got %d", mock.CallCount("ASSOCIATE group host"))
	}
	if mock.CallCount("ASSOCIATE group child") < 2 {
		t.Errorf("expected 2 child associations (web, db), got %d", mock.CallCount("ASSOCIATE group child"))
	}

	// Delete
	if err := k8sClient.Delete(ctx, cr); err != nil {
		t.Fatalf("delete CR: %v", err)
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("DELETE inventory") >= 1
	}) {
		t.Fatal("timeout: DELETE inventory never called")
	}
}
