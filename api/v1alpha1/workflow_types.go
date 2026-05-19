package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkflowNode is one step in a workflow DAG. Each node references a
// unified_job_template (typically a JobTemplate, possibly a nested
// Workflow) and has three successor lists keyed by upstream outcome.
type WorkflowNode struct {
	// Identifier is unique within this workflow. Used for graph edges
	// (other nodes reference this node by identifier in their
	// success/failure/always lists).
	// +kubebuilder:validation:Required
	Identifier string `json:"identifier"`

	// UnifiedJobTemplate is the name of a JobTemplate (preferred) or
	// nested WorkflowJobTemplate to invoke. The controller resolves
	// this to a numeric ID at reconcile.
	// +kubebuilder:validation:Required
	UnifiedJobTemplate string `json:"unifiedJobTemplate"`

	// Kind of template ref. Default is "job_template". Set to
	// "workflow_job_template" for nested workflows.
	// +kubebuilder:validation:Enum=job_template;workflow_job_template
	// +kubebuilder:default=job_template
	// +optional
	UnifiedJobTemplateKind string `json:"unifiedJobTemplateKind,omitempty"`

	// Identifiers of nodes to run when this node finishes successfully.
	// +optional
	SuccessNodes []string `json:"successNodes,omitempty"`

	// Identifiers of nodes to run when this node fails.
	// +optional
	FailureNodes []string `json:"failureNodes,omitempty"`

	// Identifiers of nodes to run regardless of outcome.
	// +optional
	AlwaysNodes []string `json:"alwaysNodes,omitempty"`

	// Free-form YAML/JSON extra_data for this node (per-node vars).
	// +optional
	ExtraData string `json:"extraData,omitempty"`
}

// WorkflowSpec mirrors Forge `/api/v2/workflow_job_templates/` plus a
// declarative list of nodes that the controller reconciles to/from
// `/workflow_job_template_nodes/`.
type WorkflowSpec struct {
	// Display name in Forge. Defaults to metadata.name.
	// +optional
	Name string `json:"name,omitempty"`

	// +optional
	Description string `json:"description,omitempty"`

	// Owning organization (by name).
	// +kubebuilder:validation:Required
	Organization string `json:"organization"`

	// Optional default inventory name. Each node can override via its
	// own job template.
	// +optional
	Inventory string `json:"inventory,omitempty"`

	// +optional
	AllowSimultaneous bool `json:"allowSimultaneous,omitempty"`

	// +optional
	AskInventoryOnLaunch bool `json:"askInventoryOnLaunch,omitempty"`

	// +optional
	AskVariablesOnLaunch bool `json:"askVariablesOnLaunch,omitempty"`

	// +optional
	AskLimitOnLaunch bool `json:"askLimitOnLaunch,omitempty"`

	// Workflow-level extra_vars (YAML or JSON).
	// +optional
	ExtraVars string `json:"extraVars,omitempty"`

	// Nodes is the DAG. Order is irrelevant — edges are by identifier.
	// +optional
	// +listType=map
	// +listMapKey=identifier
	Nodes []WorkflowNode `json:"nodes,omitempty"`

	// Optional ForgeInstance reference (for multi-cluster). Empty = default.
	// +optional
	ForgeInstance string `json:"forgeInstance,omitempty"`
}

type WorkflowStatus struct {
	// +optional
	ForgeID int64 `json:"forgeId,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Count of reconciled nodes (helps quickly check graph size from
	// `kubectl get`).
	// +optional
	NodeCount int32 `json:"nodeCount,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wf;workflow,categories=forge
// +kubebuilder:printcolumn:name="Forge ID",type=integer,JSONPath=`.status.forgeId`
// +kubebuilder:printcolumn:name="Organization",type=string,JSONPath=`.spec.organization`
// +kubebuilder:printcolumn:name="Nodes",type=integer,JSONPath=`.status.nodeCount`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Workflow is the Schema for the workflows API.
type Workflow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkflowSpec   `json:"spec,omitempty"`
	Status WorkflowStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkflowList contains a list of Workflow.
type WorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workflow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workflow{}, &WorkflowList{})
}
