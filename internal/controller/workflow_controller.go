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

const workflowFinalizer = "workflow.forge.forgeplatform.io/finalizer"

// WorkflowReconciler reconciles a Workflow CR + its DAG of nodes.
//
// Two-phase reconcile:
//   1. Workflow shell (POST/PATCH /workflow_job_templates/)
//   2. Nodes — diff CR.Spec.Nodes (keyed by Identifier) against the
//      current node list at /workflow_job_templates/{id}/workflow_nodes/.
//      Create / update / delete to converge.
//   3. Edges — for each desired node, diff success/failure/always
//      successor lists (by target node ID, resolved from identifier).
type WorkflowReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Forge  *forgeapi.Client
}

// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=workflows,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=workflows/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=forge.forgeplatform.io,resources=workflows/finalizers,verbs=update

func (r *WorkflowReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr forgev1.Workflow
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !cr.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &cr)
	}

	if !hasFinalizer(cr.Finalizers, workflowFinalizer) {
		cr.Finalizers = append(cr.Finalizers, workflowFinalizer)
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// --- Phase 1: workflow shell ---
	desired, err := r.buildDesiredShell(ctx, &cr)
	if err != nil {
		return r.markWorkflowErr(ctx, &cr, reasonResolveErr, err)
	}

	current, err := r.findExistingShell(ctx, &cr, desired.Name)
	if err != nil {
		return r.markWorkflowErr(ctx, &cr, reasonAPIError, err)
	}

	if current == nil {
		created, err := r.Forge.CreateWorkflow(ctx, desired)
		if err != nil {
			return r.markWorkflowErr(ctx, &cr, reasonAPIError, fmt.Errorf("create: %w", err))
		}
		logger.Info("created Workflow in Forge", "id", created.ID, "name", created.Name)
		current = created
	} else if !equalWorkflow(current, desired) {
		updated, err := r.Forge.UpdateWorkflow(ctx, current.ID, desired)
		if err != nil {
			return r.markWorkflowErr(ctx, &cr, reasonAPIError, fmt.Errorf("update: %w", err))
		}
		logger.Info("updated Workflow in Forge", "id", updated.ID)
		current = updated
	}

	// --- Phase 2: nodes ---
	idByIdentifier, err := r.syncNodes(ctx, &cr, current.ID)
	if err != nil {
		return r.markWorkflowErr(ctx, &cr, reasonAPIError, fmt.Errorf("nodes: %w", err))
	}

	// --- Phase 3: edges ---
	if err := r.syncEdges(ctx, &cr, idByIdentifier); err != nil {
		return r.markWorkflowErr(ctx, &cr, reasonAPIError, fmt.Errorf("edges: %w", err))
	}

	cr.Status.ForgeID = current.ID
	cr.Status.ObservedGeneration = cr.Generation
	cr.Status.NodeCount = int32(len(cr.Spec.Nodes))
	setWorkflowCondition(&cr, conditionSynced, metav1.ConditionTrue, reasonInSync, "Workflow is in sync with Forge")
	setWorkflowCondition(&cr, conditionReady, metav1.ConditionTrue, reasonInSync, "")
	if err := r.Status().Update(ctx, &cr); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

func (r *WorkflowReconciler) reconcileDelete(ctx context.Context, cr *forgev1.Workflow) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// Forge cascades nodes when the parent workflow is deleted, so a single
	// DELETE on /workflow_job_templates/{id}/ is enough.
	if cr.Status.ForgeID > 0 {
		if err := r.Forge.DeleteWorkflow(ctx, cr.Status.ForgeID); err != nil && !forgeapi.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete forge Workflow %d: %w", cr.Status.ForgeID, err)
		}
		logger.Info("deleted Workflow from Forge", "id", cr.Status.ForgeID)
	}
	cr.Finalizers = removeString(cr.Finalizers, workflowFinalizer)
	return ctrl.Result{}, r.Update(ctx, cr)
}

func (r *WorkflowReconciler) buildDesiredShell(ctx context.Context, cr *forgev1.Workflow) (*forgeapi.Workflow, error) {
	name := cr.Spec.Name
	if name == "" {
		name = cr.Name
	}

	orgID, err := r.Forge.ResolveOrganization(ctx, cr.Spec.Organization)
	if err != nil {
		return nil, fmt.Errorf("resolve organization %q: %w", cr.Spec.Organization, err)
	}
	if orgID < 0 {
		return nil, fmt.Errorf("organization %q not found in Forge", cr.Spec.Organization)
	}

	w := &forgeapi.Workflow{
		Name:                 name,
		Description:          cr.Spec.Description,
		Organization:         orgID,
		AllowSimultaneous:    cr.Spec.AllowSimultaneous,
		AskInventoryOnLaunch: cr.Spec.AskInventoryOnLaunch,
		AskVariablesOnLaunch: cr.Spec.AskVariablesOnLaunch,
		AskLimitOnLaunch:     cr.Spec.AskLimitOnLaunch,
		ExtraVars:            cr.Spec.ExtraVars,
	}

	if cr.Spec.Inventory != "" {
		invID, err := r.Forge.ResolveInventory(ctx, cr.Spec.Inventory)
		if err != nil {
			return nil, fmt.Errorf("resolve inventory %q: %w", cr.Spec.Inventory, err)
		}
		if invID < 0 {
			return nil, fmt.Errorf("inventory %q not found in Forge", cr.Spec.Inventory)
		}
		w.Inventory = &invID
	}
	return w, nil
}

func (r *WorkflowReconciler) findExistingShell(ctx context.Context, cr *forgev1.Workflow, name string) (*forgeapi.Workflow, error) {
	if cr.Status.ForgeID > 0 {
		w, err := r.Forge.GetWorkflow(ctx, cr.Status.ForgeID)
		if err == nil {
			return w, nil
		}
		if !forgeapi.IsNotFound(err) {
			return nil, err
		}
	}
	return r.Forge.FindWorkflowByName(ctx, name)
}

// syncNodes brings the Forge node set into agreement with cr.Spec.Nodes,
// returning a map of {identifier -> Forge node ID} usable for edge sync.
func (r *WorkflowReconciler) syncNodes(ctx context.Context, cr *forgev1.Workflow, workflowID int64) (map[string]int64, error) {
	currentNodes, err := r.Forge.ListWorkflowNodes(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	currentByID := map[string]forgeapi.WorkflowNode{}
	for _, n := range currentNodes {
		currentByID[n.Identifier] = n
	}

	desiredByID := map[string]*forgev1.WorkflowNode{}
	for i := range cr.Spec.Nodes {
		n := &cr.Spec.Nodes[i]
		desiredByID[n.Identifier] = n
	}

	idByIdentifier := map[string]int64{}

	// Add or update.
	for ident, dn := range desiredByID {
		ujtID, err := r.resolveUnifiedJobTemplate(ctx, dn)
		if err != nil {
			return nil, err
		}
		wantNode := &forgeapi.WorkflowNode{
			Identifier:         ident,
			UnifiedJobTemplate: ujtID,
			ExtraData:          dn.ExtraData,
		}
		if cur, ok := currentByID[ident]; ok {
			if cur.UnifiedJobTemplate != ujtID || cur.ExtraData != dn.ExtraData {
				updated, err := r.Forge.UpdateWorkflowNode(ctx, cur.ID, wantNode)
				if err != nil {
					return nil, fmt.Errorf("update node %q: %w", ident, err)
				}
				idByIdentifier[ident] = updated.ID
			} else {
				idByIdentifier[ident] = cur.ID
			}
		} else {
			created, err := r.Forge.CreateWorkflowNode(ctx, workflowID, wantNode)
			if err != nil {
				return nil, fmt.Errorf("create node %q: %w", ident, err)
			}
			idByIdentifier[ident] = created.ID
		}
	}

	// Delete removed.
	for ident, cur := range currentByID {
		if _, ok := desiredByID[ident]; !ok {
			if err := r.Forge.DeleteWorkflowNode(ctx, cur.ID); err != nil {
				return nil, fmt.Errorf("delete node %q: %w", ident, err)
			}
		}
	}
	return idByIdentifier, nil
}

func (r *WorkflowReconciler) resolveUnifiedJobTemplate(ctx context.Context, n *forgev1.WorkflowNode) (int64, error) {
	kind := n.UnifiedJobTemplateKind
	if kind == "" {
		kind = "job_template"
	}
	switch kind {
	case "job_template":
		id, err := r.Forge.ResolveJobTemplate(ctx, n.UnifiedJobTemplate)
		if err != nil {
			return 0, fmt.Errorf("resolve job_template %q: %w", n.UnifiedJobTemplate, err)
		}
		if id < 0 {
			return 0, fmt.Errorf("job_template %q not found", n.UnifiedJobTemplate)
		}
		return id, nil
	case "workflow_job_template":
		id, err := r.Forge.ResolveWorkflow(ctx, n.UnifiedJobTemplate)
		if err != nil {
			return 0, fmt.Errorf("resolve workflow %q: %w", n.UnifiedJobTemplate, err)
		}
		if id < 0 {
			return 0, fmt.Errorf("workflow %q not found", n.UnifiedJobTemplate)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("unsupported unifiedJobTemplateKind %q", kind)
	}
}

func (r *WorkflowReconciler) syncEdges(ctx context.Context, cr *forgev1.Workflow, idByIdentifier map[string]int64) error {
	for i := range cr.Spec.Nodes {
		n := &cr.Spec.Nodes[i]
		srcID, ok := idByIdentifier[n.Identifier]
		if !ok {
			continue
		}
		for _, edge := range []struct {
			name    string
			targets []string
		}{
			{"success", n.SuccessNodes},
			{"failure", n.FailureNodes},
			{"always", n.AlwaysNodes},
		} {
			if err := r.syncOneEdge(ctx, srcID, edge.name, edge.targets, idByIdentifier); err != nil {
				return fmt.Errorf("node %q edge %s: %w", n.Identifier, edge.name, err)
			}
		}
	}
	return nil
}

func (r *WorkflowReconciler) syncOneEdge(ctx context.Context, srcID int64, edge string, targets []string, idByIdentifier map[string]int64) error {
	desired := map[int64]struct{}{}
	for _, ident := range targets {
		id, ok := idByIdentifier[ident]
		if !ok {
			return fmt.Errorf("edge target %q does not match any node identifier in this workflow", ident)
		}
		desired[id] = struct{}{}
	}
	currentIDs, err := r.Forge.ListNodeEdges(ctx, srcID, edge)
	if err != nil {
		return err
	}
	current := map[int64]struct{}{}
	for _, id := range currentIDs {
		current[id] = struct{}{}
	}
	for id := range desired {
		if _, ok := current[id]; !ok {
			if err := r.Forge.AssociateNodeEdge(ctx, srcID, edge, id); err != nil {
				return err
			}
		}
	}
	for id := range current {
		if _, ok := desired[id]; !ok {
			if err := r.Forge.DisassociateNodeEdge(ctx, srcID, edge, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *WorkflowReconciler) markWorkflowErr(ctx context.Context, cr *forgev1.Workflow, reason string, err error) (ctrl.Result, error) {
	setWorkflowCondition(cr, conditionReady, metav1.ConditionFalse, reason, err.Error())
	setWorkflowCondition(cr, conditionSynced, metav1.ConditionFalse, reason, err.Error())
	if uerr := r.Status().Update(ctx, cr); uerr != nil {
		return ctrl.Result{}, uerr
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *WorkflowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&forgev1.Workflow{}).
		Complete(r)
}

func setWorkflowCondition(cr *forgev1.Workflow, condType string, status metav1.ConditionStatus, reason, msg string) {
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

func equalWorkflow(a, b *forgeapi.Workflow) bool {
	return a.Name == b.Name &&
		a.Description == b.Description &&
		a.Organization == b.Organization &&
		equalInt64Ptr(a.Inventory, b.Inventory) &&
		a.AllowSimultaneous == b.AllowSimultaneous &&
		a.AskInventoryOnLaunch == b.AskInventoryOnLaunch &&
		a.AskVariablesOnLaunch == b.AskVariablesOnLaunch &&
		a.AskLimitOnLaunch == b.AskLimitOnLaunch &&
		a.ExtraVars == b.ExtraVars
}
