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

const orgFinalizer = "organization.forge.forgeplatform.io/finalizer"

// OrganizationReconciler reconciles an Organization CR with Forge.
type OrganizationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Forge  *forgeapi.Client
	Pool   *forgeapi.ClientPool
}

// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=organizations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=organizations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=organizations/finalizers,verbs=update

func (r *OrganizationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr forgev1.Organization
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !cr.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &cr)
	}

	if !hasFinalizer(cr.Finalizers, orgFinalizer) {
		cr.Finalizers = append(cr.Finalizers, orgFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	fc, err := clientFor(ctx, r.Pool, r.Forge, cr.Namespace, cr.Spec.ForgeInstance)
	if err != nil {
		return r.markOrgErr(ctx, &cr, reasonResolveErr, fmt.Errorf("forge instance: %w", err))
	}

	desired, err := r.buildDesired(ctx, fc, &cr)
	if err != nil {
		return r.markOrgErr(ctx, &cr, reasonResolveErr, err)
	}

	current, err := r.findExisting(ctx, fc, &cr, desired.Name)
	if err != nil {
		return r.markOrgErr(ctx, &cr, reasonAPIError, err)
	}

	if current == nil {
		created, err := fc.CreateOrganization(ctx, desired)
		if err != nil {
			return r.markOrgErr(ctx, &cr, reasonAPIError, fmt.Errorf("create: %w", err))
		}
		logger.Info("created Organization in Forge", "id", created.ID, "name", created.Name)
		current = created
	} else if !equalOrganization(current, desired) {
		updated, err := fc.UpdateOrganization(ctx, current.ID, desired)
		if err != nil {
			return r.markOrgErr(ctx, &cr, reasonAPIError, fmt.Errorf("update: %w", err))
		}
		logger.Info("updated Organization in Forge", "id", updated.ID)
		current = updated
	}

	cr.Status.ForgeID = current.ID
	cr.Status.ObservedGeneration = cr.Generation
	setOrgCondition(&cr, conditionSynced, metav1.ConditionTrue, reasonInSync, "Organization is in sync with Forge")
	setOrgCondition(&cr, conditionReady, metav1.ConditionTrue, reasonInSync, "")
	if err := r.Status().Update(ctx, &cr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

func (r *OrganizationReconciler) reconcileDelete(ctx context.Context, cr *forgev1.Organization) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if cr.Status.ForgeID > 0 {
		fc, ferr := clientFor(ctx, r.Pool, r.Forge, cr.Namespace, cr.Spec.ForgeInstance)
		if ferr != nil {
			return ctrl.Result{}, fmt.Errorf("resolve forge instance for delete: %w", ferr)
		}
		if err := fc.DeleteOrganization(ctx, cr.Status.ForgeID); err != nil && !forgeapi.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete forge Organization %d: %w", cr.Status.ForgeID, err)
		}
		logger.Info("deleted Organization from Forge", "id", cr.Status.ForgeID)
	}
	cr.Finalizers = removeString(cr.Finalizers, orgFinalizer)
	return ctrl.Result{}, r.Update(ctx, cr)
}

func (r *OrganizationReconciler) buildDesired(ctx context.Context, fc *forgeapi.Client, cr *forgev1.Organization) (*forgeapi.Organization, error) {
	name := cr.Spec.Name
	if name == "" {
		name = cr.Name
	}

	o := &forgeapi.Organization{
		Name:        name,
		Description: cr.Spec.Description,
		MaxHosts:    cr.Spec.MaxHosts,
	}

	if cr.Spec.DefaultEnvironment != "" {
		eeID, err := fc.ResolveExecutionEnvironment(ctx, cr.Spec.DefaultEnvironment)
		if err != nil {
			return nil, fmt.Errorf("resolve execution_environment %q: %w", cr.Spec.DefaultEnvironment, err)
		}
		if eeID < 0 {
			return nil, fmt.Errorf("execution_environment %q not found in Forge", cr.Spec.DefaultEnvironment)
		}
		o.DefaultEnvironment = &eeID
	}
	return o, nil
}

func (r *OrganizationReconciler) findExisting(ctx context.Context, fc *forgeapi.Client, cr *forgev1.Organization, name string) (*forgeapi.Organization, error) {
	if cr.Status.ForgeID > 0 {
		o, err := fc.GetOrganization(ctx, cr.Status.ForgeID)
		if err == nil {
			return o, nil
		}
		if !forgeapi.IsNotFound(err) {
			return nil, err
		}
	}
	return fc.FindOrganizationByName(ctx, name)
}

func (r *OrganizationReconciler) markOrgErr(ctx context.Context, cr *forgev1.Organization, reason string, err error) (ctrl.Result, error) {
	setOrgCondition(cr, conditionReady, metav1.ConditionFalse, reason, err.Error())
	setOrgCondition(cr, conditionSynced, metav1.ConditionFalse, reason, err.Error())
	if uerr := r.Status().Update(ctx, cr); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *OrganizationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.Organization{}).
		Complete(r)
}

func setOrgCondition(cr *forgev1.Organization, condType string, status metav1.ConditionStatus, reason, msg string) {
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

func equalOrganization(a, b *forgeapi.Organization) bool {
	return a.Name == b.Name &&
		a.Description == b.Description &&
		a.MaxHosts == b.MaxHosts &&
		equalInt64Ptr(a.DefaultEnvironment, b.DefaultEnvironment)
}
