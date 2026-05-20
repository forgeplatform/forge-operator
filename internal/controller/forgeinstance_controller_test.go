package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	forgev1 "github.com/forgeplatform/forge-operator/api/v1alpha1"
)

func TestForgeInstanceProbe(t *testing.T) {
	mock := newMockForge()
	srv, _ := mock.start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := newManager(t, ctx, srv.URL, "test-token")
	defer stop()

	tokSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "forge-eu-token", Namespace: "default"},
		StringData: map[string]string{"token": "test-token"},
	}
	if err := k8sClient.Create(ctx, tokSecret); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	cr := &forgev1.ForgeInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "forge-eu", Namespace: "default"},
		Spec: forgev1.ForgeInstanceSpec{
			URL:                srv.URL,
			InsecureSkipVerify: true,
			TokenSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "forge-eu-token"},
				Key:                  "token",
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	if !pollUntil(t, 10*time.Second, func() bool {
		var got forgev1.ForgeInstance
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "forge-eu", Namespace: "default"}, &got); err != nil {
			return false
		}
		return got.Status.Reachable && got.Status.ServerVersion != ""
	}) {
		t.Fatal("timeout: ForgeInstance status never set reachable")
	}
	if mock.CallCount("GET ping") < 1 {
		t.Fatal("expected GET ping")
	}
}
