package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretKeyRef points at a key inside a k8s Secret in the same namespace
// as the Credential CR. Used for sensitive credential fields (passwords,
// private keys, tokens) so they never appear in the CR YAML directly.
type SecretKeyRef struct {
	// Name of the Secret in the same namespace as the Credential.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key inside the Secret's data block.
	// +kubebuilder:validation:Required
	Key string `json:"key"`
}

// CredentialInputFromSecret maps one Forge input field name to a Secret
// key. Example: ssh_key_data → Secret 'deploy-key' / key 'id_rsa'.
type CredentialInputFromSecret struct {
	// Forge input field name (e.g. "username", "password", "ssh_key_data",
	// "vault_token"). The set of valid names depends on credentialType.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	ValueFrom SecretKeyRef `json:"valueFrom"`
}

// CredentialSpec defines the desired state of a Forge Credential.
type CredentialSpec struct {
	// +optional
	Name string `json:"name,omitempty"`

	// +optional
	Description string `json:"description,omitempty"`

	// +kubebuilder:validation:Required
	Organization string `json:"organization"`

	// CredentialType is the Forge built-in or custom type name, e.g.:
	// "Machine", "Source Control", "Vault", "Amazon Web Services",
	// "Container Registry", "GitHub Personal Access Token".
	// +kubebuilder:validation:Required
	CredentialType string `json:"credentialType"`

	// Inputs is a free-form map of non-sensitive fields. Field names
	// must match the credentialType schema (e.g. {username: deploy}
	// for Machine credential).
	// +optional
	Inputs map[string]string `json:"inputs,omitempty"`

	// InputsFrom pulls sensitive fields from a k8s Secret. Each entry
	// maps a Forge input field name to a Secret/key.
	// +optional
	InputsFrom []CredentialInputFromSecret `json:"inputsFrom,omitempty"`
}

// CredentialStatus reflects the observed Forge state.
type CredentialStatus struct {
	// +optional
	ForgeID int64 `json:"forgeId,omitempty"`

	// CredentialTypeID is the resolved numeric ID of the credentialType
	// (cached so we don't look it up every reconcile).
	// +optional
	CredentialTypeID int64 `json:"credentialTypeId,omitempty"`

	// SecretsHash is a SHA256 of the resolved (sensitive + non-sensitive)
	// inputs at last successful sync. Used to detect Secret rotation —
	// when the hash changes, the operator pushes a fresh PATCH to Forge.
	// +optional
	SecretsHash string `json:"secretsHash,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cred,categories=forge
// +kubebuilder:printcolumn:name="Forge ID",type=integer,JSONPath=`.status.forgeId`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.credentialType`
// +kubebuilder:printcolumn:name="Org",type=string,JSONPath=`.spec.organization`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Credential is the Schema for the credentials API.
type Credential struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CredentialSpec   `json:"spec,omitempty"`
	Status CredentialStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CredentialList contains a list of Credential.
type CredentialList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Credential `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Credential{}, &CredentialList{})
}
