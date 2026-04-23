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

func TestCredentialLifecycle(t *testing.T) {
	mock := newMockForge()
	srv, _ := mock.start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := newManager(t, ctx, srv.URL, "test-token")
	defer stop()

	// Pre-create the Secret holding the SSH key.
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ssh-key", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"ssh_key_data": []byte("PRIVATE-KEY-V1"),
		},
	}
	if err := k8sClient.Create(ctx, sec); err != nil {
		t.Fatalf("create Secret: %v", err)
	}

	cr := &forgev1.Credential{
		ObjectMeta: metav1.ObjectMeta{Name: "deploy-key", Namespace: "default"},
		Spec: forgev1.CredentialSpec{
			Description:    "machine cred",
			Organization:   "Default",
			CredentialType: "Machine",
			Inputs:         map[string]string{"username": "deploy"},
			InputsFrom: []forgev1.CredentialInputFromSecret{
				{Name: "ssh_key_data", ValueFrom: forgev1.SecretKeyRef{Name: "ssh-key", Key: "ssh_key_data"}},
			},
		},
	}
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatalf("create CR: %v", err)
	}

	// Wait for creation in Forge.
	if !pollUntil(t, 15*time.Second, func() bool {
		var got forgev1.Credential
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "deploy-key", Namespace: "default"}, &got); err != nil {
			return false
		}
		return got.Status.ForgeID > 0 && got.Status.SecretsHash != ""
	}) {
		t.Fatal("timeout: credential ForgeID/SecretsHash not set")
	}

	if mock.CallCount("POST credentials") != 1 {
		t.Errorf("expected exactly 1 POST credentials, got %d", mock.CallCount("POST credentials"))
	}

	// Verify mock received both literal and secret-derived inputs.
	mock.mu.Lock()
	var stored map[string]any
	for _, c := range mock.credentials {
		stored = c
		break
	}
	mock.mu.Unlock()
	inputs, _ := stored["inputs"].(map[string]any)
	if inputs["username"] != "deploy" {
		t.Errorf("inputs.username want %q got %v", "deploy", inputs["username"])
	}
	if inputs["ssh_key_data"] != "PRIVATE-KEY-V1" {
		t.Errorf("inputs.ssh_key_data want PRIVATE-KEY-V1 got %v", inputs["ssh_key_data"])
	}

	// --- Rotate the Secret: new key value should trigger a PATCH ---
	patchBaseline := mock.CallCount("PATCH credential")

	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "ssh-key", Namespace: "default"}, sec); err != nil {
		t.Fatalf("re-get Secret: %v", err)
	}
	sec.Data["ssh_key_data"] = []byte("PRIVATE-KEY-V2")
	if err := k8sClient.Update(ctx, sec); err != nil {
		t.Fatalf("update Secret: %v", err)
	}
	// Force reconcile by touching the CR — Secret events are not watched
	// in this MVP, so we trigger a CR reconcile manually. (The 60s
	// periodic requeue would also catch it eventually.)
	var cred forgev1.Credential
	_ = k8sClient.Get(ctx, types.NamespacedName{Name: "deploy-key", Namespace: "default"}, &cred)
	if cred.Annotations == nil {
		cred.Annotations = map[string]string{}
	}
	cred.Annotations["touch"] = "1"
	if err := k8sClient.Update(ctx, &cred); err != nil {
		t.Fatalf("touch CR: %v", err)
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("PATCH credential") > patchBaseline
	}) {
		t.Fatalf("timeout: PATCH credential after secret rotation (baseline=%d)", patchBaseline)
	}
	// Wait for the rotated value to actually land in the mock — there
	// may be one more reconcile in flight that lags behind the count.
	if !pollUntil(t, 5*time.Second, func() bool {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		for _, c := range mock.credentials {
			inputs, _ := c["inputs"].(map[string]any)
			if inputs["ssh_key_data"] == "PRIVATE-KEY-V2" {
				return true
			}
		}
		return false
	}) {
		t.Fatal("timeout: rotated ssh_key_data never reached mock")
	}

	// (rotation already verified by the pollUntil above)

	// Delete
	if err := k8sClient.Delete(ctx, &cred); err != nil {
		t.Fatalf("delete CR: %v", err)
	}
	if !pollUntil(t, 10*time.Second, func() bool {
		return mock.CallCount("DELETE credential") >= 1
	}) {
		t.Fatal("timeout: DELETE credential never called")
	}
}
