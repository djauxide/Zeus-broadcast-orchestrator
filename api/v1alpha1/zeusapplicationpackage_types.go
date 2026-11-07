package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MediaIO struct {
	MXLReader bool     `json:"mxlReader,omitempty"`
	MXLWriter bool     `json:"mxlWriter,omitempty"`
	DomainRef string   `json:"domainRef,omitempty"`
	Formats   []string `json:"formats,omitempty"`
}

type CRMResources struct {
	CPU          string   `json:"cpu"`
	Memory       string   `json:"memory"`
	Accelerators []string `json:"accelerators,omitempty"`
	MediaIO      MediaIO  `json:"mediaIO,omitempty"`
}

type NMOSSpec struct {
	IS04          bool   `json:"is04,omitempty"`
	IS05          bool   `json:"is05,omitempty"`
	IS10          bool   `json:"is10,omitempty"`
	Transport     string `json:"transport,omitempty"`
	SenderMatch   bool   `json:"senderMatch,omitempty"`   // Require IS-04 sender discovery before validation
	MediaType     string `json:"mediaType,omitempty"`     // e.g. "video/raw"
	Essence       string `json:"essence,omitempty"`       // e.g. "raw"
	Sampling      string `json:"sampling,omitempty"`      // e.g. "YCbCr:4:2:2:10bit"
}

// RoutingConfig specifies how a NAP should be routed through a routing router.
type RoutingConfig struct {
	// Provider is the routing protocol provider (tcp, swp08, emberplus).
	Provider string `json:"provider"`

	// RouterRef is a reference to the routing router.
	RouterRef string `json:"routerRef,omitempty"`

	// SourceID is the source crosspoint ID on the router.
	SourceID string `json:"sourceID,omitempty"`

	// DestinationID is the destination crosspoint ID on the router.
	DestinationID string `json:"destinationID,omitempty"`
}

type ZeusApplicationPackageSpec struct {
	Image          string       `json:"image"`
	ImageSignature string       `json:"imageSignature"`
	CRM            CRMResources `json:"crm"`
	NMOS           NMOSSpec     `json:"nmos,omitempty"`
	Routing        RoutingConfig `json:"routing,omitempty"`
	HealthPath     string       `json:"healthPath,omitempty"`
}

type ZeusApplicationPackageStatus struct {
	Phase       string             `json:"phase,omitempty"`
	Conditions  []metav1.Condition `json:"conditions,omitempty"`
	ObservedGen int64              `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
type ZeusApplicationPackage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ZeusApplicationPackageSpec   `json:"spec,omitempty"`
	Status            ZeusApplicationPackageStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ZeusApplicationPackageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZeusApplicationPackage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZeusApplicationPackage{}, &ZeusApplicationPackageList{})
}
