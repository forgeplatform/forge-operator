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

const projectFinalizer = "project.forge.forgeplatform.io/finalizer"

// ProjectReconciler reconciles a Project CR with Forge.
type ProjectReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Forge is the default client (global FORGE_URL/TOKEN). Used when
	// spec.forgeInstance is empty.
	Forge *forgeapi.Client
	// Pool dispenses per-ForgeInstance clients for multi-cluster CRs.
	// Nil pool falls back to Forge.
	Pool *forgeapi.ClientPool
}

// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=projects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=projects/finalizers,verbs=update

func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr forgev1.Project
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !cr.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &cr)
	}

	if !hasFinalizer(cr.Finalizers, projectFinalizer) {
		cr.Finalizers = append(cr.Finalizers, projectFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	fc, err := clientFor(ctx, r.Pool, r.Forge, cr.Namespace, cr.Spec.ForgeInstance)
	if err != nil {
		return r.markProjectErr(ctx, &cr, reasonResolveErr, fmt.Errorf("forge instance: %w", err))
	}

	desired, err := r.buildDesired(ctx, fc, &cr)
	if err != nil {
		return r.markProjectErr(ctx, &cr, reasonResolveErr, err)
	}

	current, err := r.findExisting(ctx, fc, &cr, desired.Name)
	if err != nil {
		return r.markProjectErr(ctx, &cr, reasonAPIError, err)
	}

	if current == nil {
		created, err := fc.CreateProject(ctx, desired)
		if err != nil {
			return r.markProjectErr(ctx, &cr, reasonAPIError, fmt.Errorf("create: %w", err))
		}
		logger.Info("created Project in Forge", "id", created.ID, "name", created.Name)
		current = created
	} else if !equalProject(current, desired) {
		updated, err := fc.UpdateProject(ctx, current.ID, desired)
		if err != nil {
			return r.markProjectErr(ctx, &cr, reasonAPIError, fmt.Errorf("update: %w", err))
		}
		logger.Info("updated Project in Forge", "id", updated.ID)
		current = updated
	}

	cr.Status.ForgeID = current.ID
	cr.Status.ObservedGeneration = cr.Generation
	cr.Status.ScmRevision = current.ScmRevision
	setProjectCondition(&cr, conditionSynced, metav1.ConditionTrue, reasonInSync, "Project is in sync with Forge")
	setProjectCondition(&cr, conditionReady, metav1.ConditionTrue, reasonInSync, "")
	if err := r.Status().Update(ctx, &cr); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

func (r *ProjectReconciler) reconcileDelete(ctx context.Context, cr *forgev1.Project) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if cr.Status.ForgeID > 0 {
		fc, ferr := clientFor(ctx, r.Pool, r.Forge, cr.Namespace, cr.Spec.ForgeInstance)
		if ferr != nil {
			return ctrl.Result{}, fmt.Errorf("resolve forge instance for delete: %w", ferr)
		}
		if err := fc.DeleteProject(ctx, cr.Status.ForgeID); err != nil && !forgeapi.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete forge Project %d: %w", cr.Status.ForgeID, err)
		}
		logger.Info("deleted Project from Forge", "id", cr.Status.ForgeID)
	}
	cr.Finalizers = removeString(cr.Finalizers, projectFinalizer)
	return ctrl.Result{}, r.Update(ctx, cr)
}

func (r *ProjectReconciler) buildDesired(ctx context.Context, fc *forgeapi.Client, cr *forgev1.Project) (*forgeapi.Project, error) {
	name := cr.Spec.Name
	if name == "" {
		name = cr.Name
	}

	orgID, err := fc.ResolveOrganization(ctx, cr.Spec.Organization)
	if err != nil {
		return nil, fmt.Errorf("resolve organization %q: %w", cr.Spec.Organization, err)
	}
	if orgID < 0 {
		return nil, fmt.Errorf("organization %q not found in Forge", cr.Spec.Organization)
	}

	scmType := cr.Spec.ScmType
	if scmType == "" {
		scmType = "git"
	}
	if scmType == "manual" {
		scmType = ""
	}

	p := &forgeapi.Project{
		Name:                  name,
		Description:           cr.Spec.Description,
		Organization:          orgID,
		ScmType:               scmType,
		ScmURL:                cr.Spec.ScmURL,
		ScmBranch:             cr.Spec.ScmBranch,
		ScmRefspec:            cr.Spec.ScmRefspec,
		ScmClean:              cr.Spec.ScmClean,
		ScmDeleteOnUpdate:     cr.Spec.ScmDeleteOnUpdate,
		ScmUpdateOnLaunch:     cr.Spec.ScmUpdateOnLaunch,
		ScmUpdateCacheTimeout: cr.Spec.ScmUpdateCacheTimeout,
		AllowOverride:         cr.Spec.AllowOverride,
		Timeout:               cr.Spec.Timeout,
	}

	if cr.Spec.ScmCredential != "" {
		credID, err := fc.ResolveCredential(ctx, cr.Spec.ScmCredential)
		if err != nil {
			return nil, fmt.Errorf("resolve credential %q: %w", cr.Spec.ScmCredential, err)
		}
		if credID < 0 {
			return nil, fmt.Errorf("credential %q not found in Forge", cr.Spec.ScmCredential)
		}
		p.Credential = &credID
	}

	if cr.Spec.DefaultEnvironment != "" {
		eeID, err := fc.ResolveExecutionEnvironment(ctx, cr.Spec.DefaultEnvironment)
		if err != nil {
			return nil, fmt.Errorf("resolve execution_environment %q: %w", cr.Spec.DefaultEnvironment, err)
		}
		if eeID < 0 {
			return nil, fmt.Errorf("execution_environment %q not found in Forge", cr.Spec.DefaultEnvironment)
		}
		p.DefaultEnvironment = &eeID
	}

	return p, nil
}

func (r *ProjectReconciler) findExisting(ctx context.Context, fc *forgeapi.Client, cr *forgev1.Project, name string) (*forgeapi.Project, error) {
	if cr.Status.ForgeID > 0 {
		p, err := fc.GetProject(ctx, cr.Status.ForgeID)
		if err == nil {
			return p, nil
		}
		if !forgeapi.IsNotFound(err) {
			return nil, err
		}
	}
	return fc.FindProjectByName(ctx, name)
}

func (r *ProjectReconciler) markProjectErr(ctx context.Context, cr *forgev1.Project, reason string, err error) (ctrl.Result, error) {
	setProjectCondition(cr, conditionReady, metav1.ConditionFalse, reason, err.Error())
	setProjectCondition(cr, conditionSynced, metav1.ConditionFalse, reason, err.Error())
	if uerr := r.Status().Update(ctx, cr); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.Project{}).
		Complete(r)
}

// --- helpers ---

func setProjectCondition(cr *forgev1.Project, condType string, status metav1.ConditionStatus, reason, msg string) {
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

func equalProject(a, b *forgeapi.Project) bool {
	return a.Name == b.Name &&
		a.Description == b.Description &&
		a.Organization == b.Organization &&
		a.ScmType == b.ScmType &&
		a.ScmURL == b.ScmURL &&
		a.ScmBranch == b.ScmBranch &&
		a.ScmRefspec == b.ScmRefspec &&
		equalInt64Ptr(a.Credential, b.Credential) &&
		a.ScmClean == b.ScmClean &&
		a.ScmDeleteOnUpdate == b.ScmDeleteOnUpdate &&
		a.ScmUpdateOnLaunch == b.ScmUpdateOnLaunch &&
		a.ScmUpdateCacheTimeout == b.ScmUpdateCacheTimeout &&
		a.AllowOverride == b.AllowOverride &&
		a.Timeout == b.Timeout &&
		equalInt64Ptr(a.DefaultEnvironment, b.DefaultEnvironment)
}

func equalInt64Ptr(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
