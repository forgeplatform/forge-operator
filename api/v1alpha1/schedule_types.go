package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScheduleSpec defines the desired state of a Forge Schedule.
//
// Forge schedules attach to a "unified job template" (JobTemplate or
// WorkflowJobTemplate). MVP supports only JobTemplate references.
type ScheduleSpec struct {
	// +optional
	Name string `json:"name,omitempty"`

	// +optional
	Description string `json:"description,omitempty"`

	// JobTemplate is the name of the JobTemplate (Forge entity) this
	// schedule fires. Operator resolves it to a numeric ID.
	// +kubebuilder:validation:Required
	JobTemplate string `json:"jobTemplate"`

	// RRule is an RFC 5545 recurrence rule. Examples:
	//   "DTSTART;TZID=America/New_York:20260101T090000 RRULE:FREQ=DAILY;INTERVAL=1"
	//   "DTSTART:20260101T000000Z RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR"
	// +kubebuilder:validation:Required
	RRule string `json:"rrule"`

	// Whether the schedule is active. Defaults to true.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Free-form YAML/JSON passed as extra_data overrides at launch
	// time. Same shape as JobTemplate.spec.extraVars.
	// +optional
	ExtraData string `json:"extraData,omitempty"`
}

// ScheduleStatus reflects the observed Forge state.
type ScheduleStatus struct {
	// +optional
	ForgeID int64 `json:"forgeId,omitempty"`

	// JobTemplateID is the resolved Forge ID of the referenced JT.
	// +optional
	JobTemplateID int64 `json:"jobTemplateId,omitempty"`

	// NextRun is the next scheduled execution time as reported by Forge.
	// +optional
	NextRun string `json:"nextRun,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sch,categories=forge
// +kubebuilder:printcolumn:name="Forge ID",type=integer,JSONPath=`.status.forgeId`
// +kubebuilder:printcolumn:name="JobTemplate",type=string,JSONPath=`.spec.jobTemplate`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Next Run",type=string,JSONPath=`.status.nextRun`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Schedule is the Schema for the schedules API.
type Schedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScheduleSpec   `json:"spec,omitempty"`
	Status ScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ScheduleList contains a list of Schedule.
type ScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Schedule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Schedule{}, &ScheduleList{})
}
