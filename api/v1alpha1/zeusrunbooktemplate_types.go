package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunbookTemplateSpec defines the desired state of a runbook template.
type RunbookTemplateSpec struct {
	// DisplayName is a human-readable name for the template.
	DisplayName string `json:"displayName"`

	// Description describes what the template is used for.
	Description string `json:"description,omitempty"`

	// EventClass is the default event class for runbooks created from this template.
	EventClass string `json:"eventClass"`

	// NAPs is the default NAP placement configuration.
	NAPs []NAPPlacement `json:"naps,omitempty"`

	// Ingress is the default ingress configuration.
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// Parameters defines template parameters that can be overridden when instantiating.
	Parameters []TemplateParameter `json:"parameters,omitempty"`

	// Example values for each parameter.
	Examples map[string]string `json:"examples,omitempty"`
}

// TemplateParameter defines a parameter that can be overridden when
// instantiating a runbook from a template.
type TemplateParameter struct {
	// Name is the parameter name.
	Name string `json:"name"`

	// Description describes what the parameter does.
	Description string `json:"description,omitempty"`

	// Default is the default value if not overridden.
	Default string `json:"default,omitempty"`

	// Required indicates whether the parameter must be provided.
	Required bool `json:"required"`

	// Type is the parameter type (string, int, bool).
	Type string `json:"type"`
}

// RunbookTemplateStatus defines the observed state of a runbook template.
type RunbookTemplateStatus struct {
	// InstantiatedCount is the number of runbooks created from this template.
	InstantiatedCount int64 `json:"instantiatedCount,omitempty"`

	// LastInstantiatedAt is the timestamp of the last instantiation.
	LastInstantiatedAt *metav1.Time `json:"lastInstantiatedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="EventClass",type=string,JSONPath=`.spec.eventClass`
// +kubebuilder:printcolumn:name="NAPs",type=integer,JSONPath=`.spec.naps|length`
// +kubebuilder:printcolumn:name="Parameters",type=integer,JSONPath=`.spec.parameters|length`
type RunbookTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunbookTemplateSpec   `json:"spec,omitempty"`
	Status RunbookTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type RunbookTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunbookTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RunbookTemplate{}, &RunbookTemplateList{})
}
