package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NAPRegistryPhase is the phase of a NAP registry entry.
type NAPRegistryPhase string

const (
	// NAPRegistryPending means the registration is awaiting approval.
	NAPRegistryPending NAPRegistryPhase = "Pending"
	// NAPRegistryApproved means the registration has been approved.
	NAPRegistryApproved NAPRegistryPhase = "Approved"
	// NAPRegistryRejected means the registration was rejected.
	NAPRegistryRejected NAPRegistryPhase = "Rejected"
)

// NAPRegistrySpec defines the desired state of a NAP registry entry.
type NAPRegistrySpec struct {
	// NAPName is the name of the NAP being registered.
	NAPName string `json:"napName"`

	// NAPVersion is the version of the NAP.
	NAPVersion string `json:"napVersion"`

	// NAPImage is the container image for the NAP.
	NAPImage string `json:"napImage"`

	// NAPImageSignature is the signature for the NAP image.
	NAPImageSignature string `json:"napImageSignature"`

	// NAPType is the type of NAP (e.g., "vision-mixer", "audio-processor").
	NAPType string `json:"napType"`

	// NAPDescription is a human-readable description of the NAP.
	NAPDescription string `json:"napDescription,omitempty"`

	// ConformanceResult is the result of the NAP conformance suite.
	ConformanceResult string `json:"conformanceResult,omitempty"`

	// ConformanceReport is a reference to the conformance report.
	ConformanceReport string `json:"conformanceReport,omitempty"`

	// Submitter is the entity that submitted the registration.
	Submitter string `json:"submitter"`

	// SubmitterContact is contact information for the submitter.
	SubmitterContact string `json:"submitterContact,omitempty"`
}

// NAPRegistryStatus defines the observed state of a NAP registry entry.
type NAPRegistryStatus struct {
	// Phase is the current phase of the registration.
	Phase NAPRegistryPhase `json:"phase,omitempty"`

	// Conditions describe the current state of the registration.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ApprovedAt is the timestamp when the registration was approved.
	ApprovedAt *metav1.Time `json:"approvedAt,omitempty"`

	// RejectedAt is the timestamp when the registration was rejected.
	RejectedAt *metav1.Time `json:"rejectedAt,omitempty"`

	// RejectionReason is the reason for rejection, if applicable.
	RejectionReason string `json:"rejectionReason,omitempty"`

	// ObservedGeneration is the generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="NAPName",type=string,JSONPath=`.spec.napName`
// +kubebuilder:printcolumn:name="NAPVersion",type=string,JSONPath=`.spec.napVersion`
// +kubebuilder:printcolumn:name="NAPType",type=string,JSONPath=`.spec.napType`
// +kubebuilder:printcolumn:name="Submitter",type=string,JSONPath=`.spec.submitter`
type NAPRegistry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NAPRegistrySpec   `json:"spec,omitempty"`
	Status NAPRegistryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type NAPRegistryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NAPRegistry `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NAPRegistry{}, &NAPRegistryList{})
}
