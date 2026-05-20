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

const teamFinalizer = "team.forge.forgeplatform.io/finalizer"

// TeamReconciler reconciles a Team CR with Forge, including the
// /teams/{id}/users/ membership M2M relation.
type TeamReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Forge  *forgeapi.Client
	Pool   *forgeapi.ClientPool
}

// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=teams,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=teams/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=teams/finalizers,verbs=update

func (r *TeamReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr forgev1.Team
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !cr.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &cr)
	}

	if !hasFinalizer(cr.Finalizers, teamFinalizer) {
		cr.Finalizers = append(cr.Finalizers, teamFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	fc, err := clientFor(ctx, r.Pool, r.Forge, cr.Namespace, cr.Spec.ForgeInstance)
	if err != nil {
		return r.markTeamErr(ctx, &cr, reasonResolveErr, fmt.Errorf("forge instance: %w", err))
	}

	desired, err := r.buildDesired(ctx, fc, &cr)
	if err != nil {
		return r.markTeamErr(ctx, &cr, reasonResolveErr, err)
	}

	current, err := r.findExisting(ctx, fc, &cr, desired.Name)
	if err != nil {
		return r.markTeamErr(ctx, &cr, reasonAPIError, err)
	}

	if current == nil {
		created, err := fc.CreateTeam(ctx, desired)
		if err != nil {
			return r.markTeamErr(ctx, &cr, reasonAPIError, fmt.Errorf("create: %w", err))
		}
		logger.Info("created Team in Forge", "id", created.ID, "name", created.Name)
		current = created
	} else if !equalTeam(current, desired) {
		updated, err := fc.UpdateTeam(ctx, current.ID, desired)
		if err != nil {
			return r.markTeamErr(ctx, &cr, reasonAPIError, fmt.Errorf("update: %w", err))
		}
		logger.Info("updated Team in Forge", "id", updated.ID)
		current = updated
	}

	if err := r.syncUsers(ctx, fc, &cr, current.ID); err != nil {
		return r.markTeamErr(ctx, &cr, reasonAPIError, fmt.Errorf("users: %w", err))
	}

	cr.Status.ForgeID = current.ID
	cr.Status.ObservedGeneration = cr.Generation
	setTeamCondition(&cr, conditionSynced, metav1.ConditionTrue, reasonInSync, "Team is in sync with Forge")
	setTeamCondition(&cr, conditionReady, metav1.ConditionTrue, reasonInSync, "")
	if err := r.Status().Update(ctx, &cr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

func (r *TeamReconciler) reconcileDelete(ctx context.Context, cr *forgev1.Team) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if cr.Status.ForgeID > 0 {
		fc, ferr := clientFor(ctx, r.Pool, r.Forge, cr.Namespace, cr.Spec.ForgeInstance)
		if ferr != nil {
			return ctrl.Result{}, fmt.Errorf("resolve forge instance for delete: %w", ferr)
		}
		if err := fc.DeleteTeam(ctx, cr.Status.ForgeID); err != nil && !forgeapi.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete forge Team %d: %w", cr.Status.ForgeID, err)
		}
		logger.Info("deleted Team from Forge", "id", cr.Status.ForgeID)
	}
	cr.Finalizers = removeString(cr.Finalizers, teamFinalizer)
	return ctrl.Result{}, r.Update(ctx, cr)
}

func (r *TeamReconciler) buildDesired(ctx context.Context, fc *forgeapi.Client, cr *forgev1.Team) (*forgeapi.Team, error) {
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

	return &forgeapi.Team{
		Name:         name,
		Description:  cr.Spec.Description,
		Organization: orgID,
	}, nil
}

func (r *TeamReconciler) findExisting(ctx context.Context, fc *forgeapi.Client, cr *forgev1.Team, name string) (*forgeapi.Team, error) {
	if cr.Status.ForgeID > 0 {
		t, err := fc.GetTeam(ctx, cr.Status.ForgeID)
		if err == nil {
			return t, nil
		}
		if !forgeapi.IsNotFound(err) {
			return nil, err
		}
	}
	return fc.FindTeamByName(ctx, name)
}

func (r *TeamReconciler) syncUsers(ctx context.Context, fc *forgeapi.Client, cr *forgev1.Team, teamID int64) error {
	desired := map[int64]struct{}{}
	for _, username := range cr.Spec.Users {
		uid, err := fc.ResolveUser(ctx, username)
		if err != nil {
			return fmt.Errorf("resolve user %q: %w", username, err)
		}
		if uid < 0 {
			return fmt.Errorf("user %q not found in Forge", username)
		}
		desired[uid] = struct{}{}
	}

	currentIDs, err := fc.ListTeamUsers(ctx, teamID)
	if err != nil {
		return err
	}
	current := map[int64]struct{}{}
	for _, id := range currentIDs {
		current[id] = struct{}{}
	}

	for id := range desired {
		if _, ok := current[id]; !ok {
			if err := fc.AssociateTeamUser(ctx, teamID, id); err != nil {
				return fmt.Errorf("associate user %d: %w", id, err)
			}
		}
	}
	for id := range current {
		if _, ok := desired[id]; !ok {
			if err := fc.DisassociateTeamUser(ctx, teamID, id); err != nil {
				return fmt.Errorf("disassociate user %d: %w", id, err)
			}
		}
	}
	return nil
}

func (r *TeamReconciler) markTeamErr(ctx context.Context, cr *forgev1.Team, reason string, err error) (ctrl.Result, error) {
	setTeamCondition(cr, conditionReady, metav1.ConditionFalse, reason, err.Error())
	setTeamCondition(cr, conditionSynced, metav1.ConditionFalse, reason, err.Error())
	if uerr := r.Status().Update(ctx, cr); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *TeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.Team{}).
		Complete(r)
}

func setTeamCondition(cr *forgev1.Team, condType string, status metav1.ConditionStatus, reason, msg string) {
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

func equalTeam(a, b *forgeapi.Team) bool {
	return a.Name == b.Name &&
		a.Description == b.Description &&
		a.Organization == b.Organization
}
