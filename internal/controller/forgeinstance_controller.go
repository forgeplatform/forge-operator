package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	forgev1 "github.com/forgeplatform/forge-operator/api/v1alpha1"
	"github.com/forgeplatform/forge-operator/internal/forgeapi"
)

// ForgeInstanceReconciler probes a Forge backend referenced by a
// ForgeInstance CR and surfaces reachability + server version in status.
//
// It does NOT create or mutate anything in Forge; spec changes only flow
// through the cache invalidation hook on the ClientPool.
type ForgeInstanceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Pool   *forgeapi.ClientPool
}

// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=forgeinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=forgeinstances/status,verbs=get;update;patch

func (r *ForgeInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr forgev1.ForgeInstance
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			// Drop any cached client for this name so a future re-create rebuilds.
			r.Pool.Invalidate(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Force cache rebuild on every reconcile — ClientPool gates by
	// Generation, so an unchanged spec is essentially free here.
	r.Pool.Invalidate(cr.Namespace, cr.Name)

	c, err := r.Pool.For(ctx, cr.Namespace, cr.Name)
	if err != nil {
		now := metav1.Now()
		cr.Status.Reachable = false
		cr.Status.LastChecked = &now
		setForgeInstanceCondition(&cr, conditionReady, metav1.ConditionFalse, "Resolve", err.Error())
		if uerr := r.Status().Update(ctx, &cr); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	version, err := c.Ping(ctx)
	now := metav1.Now()
	cr.Status.LastChecked = &now
	if err != nil {
		cr.Status.Reachable = false
		setForgeInstanceCondition(&cr, conditionReady, metav1.ConditionFalse, "Unreachable", fmt.Sprintf("ping: %v", err))
		if uerr := r.Status().Update(ctx, &cr); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	cr.Status.Reachable = true
	cr.Status.ServerVersion = version
	setForgeInstanceCondition(&cr, conditionReady, metav1.ConditionTrue, "Healthy", "Forge reachable")
	if err := r.Status().Update(ctx, &cr); err != nil {
		return ctrl.Result{}, err
	}
	logger.V(1).Info("probed Forge instance", "url", cr.Spec.URL, "version", version)

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

func (r *ForgeInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.ForgeInstance{}).
		Complete(r)
}

func setForgeInstanceCondition(cr *forgev1.ForgeInstance, condType string, status metav1.ConditionStatus, reason, msg string) {
	now := metav1.Now()
	for i, c := range cr.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				cr.Status.Conditions[i].LastTransitionTime = now
			}
			cr.Status.Conditions[i].Status = status
			cr.Status.Conditions[i].Reason = reason
			cr.Status.Conditions[i].Message = msg
			cr.Status.Conditions[i].ObservedGeneration = cr.Generation
			return
		}
	}
	cr.Status.Conditions = append(cr.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: now,
		ObservedGeneration: cr.Generation,
	})
}
