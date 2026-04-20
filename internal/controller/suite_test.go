package controller

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	forgev1 "github.com/forgeplatform/forge-operator/api/v1alpha1"
)

// Shared envtest fixtures, started once per test binary run.
var (
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	scheme    = clientgoscheme.Scheme
)

// TestMain starts envtest before tests, tears down after.
//
// Requires:
//
//	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
//	export KUBEBUILDER_ASSETS=$(setup-envtest use 1.30 -p path)
func TestMain(m *testing.M) {
	logf.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")

	utilruntime.Must(forgev1.AddToScheme(scheme))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(root, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		panic("envtest start: " + err.Error())
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		_ = testEnv.Stop()
		panic("client.New: " + err.Error())
	}

	code := m.Run()

	_ = testEnv.Stop()
	os.Exit(code)
}

// newManager wires the four reconcilers against the envtest apiserver
// using the given Forge HTTP base URL. Returns a stop func.
//
// Each test gets its own controller-name suffix to avoid the
// "controller with name X already exists" collision when multiple
// tests run sequentially in the same process.
func newManager(t *testing.T, ctx context.Context, forgeURL, forgeToken string) func() {
	t.Helper()

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	fc := newTestForgeClient(forgeURL, forgeToken)

	suffix := "-" + t.Name()

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.JobTemplate{}).
		Named("jobtemplate" + suffix).
		Complete(&JobTemplateReconciler{Client: mgr.GetClient(), Scheme: scheme, Forge: fc}); err != nil {
		t.Fatalf("setup jobtemplate: %v", err)
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.Inventory{}).
		Named("inventory" + suffix).
		Complete(&InventoryReconciler{Client: mgr.GetClient(), Scheme: scheme, Forge: fc}); err != nil {
		t.Fatalf("setup inventory: %v", err)
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.Credential{}).
		Named("credential" + suffix).
		Complete(&CredentialReconciler{Client: mgr.GetClient(), Scheme: scheme, Forge: fc}); err != nil {
		t.Fatalf("setup credential: %v", err)
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.Schedule{}).
		Named("schedule" + suffix).
		Complete(&ScheduleReconciler{Client: mgr.GetClient(), Scheme: scheme, Forge: fc}); err != nil {
		t.Fatalf("setup schedule: %v", err)
	}

	mgrCtx, cancel := context.WithCancel(ctx)
	mgrDone := make(chan struct{})
	go func() {
		defer close(mgrDone)
		if err := mgr.Start(mgrCtx); err != nil {
			t.Logf("manager exited: %v", err)
		}
	}()
	// Give the cache a beat to sync.
	time.Sleep(500 * time.Millisecond)
	return func() {
		cancel()
		<-mgrDone
	}
}

// pollUntil waits up to timeout for cond to return true.
func pollUntil(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
