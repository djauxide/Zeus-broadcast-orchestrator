package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SRTMode string

const (
	SRTCaller     SRTMode = "caller"
	SRTListener   SRTMode = "listener"
	SRTRendezvous SRTMode = "rendezvous"
)

type SRTSpec struct {
	Mode                SRTMode                  `json:"mode"`
	RemoteHost          string                   `json:"remoteHost,omitempty"`
	RemotePort          int32                    `json:"remotePort,omitempty"`
	LocalPort           int32                    `json:"localPort,omitempty"`
	LatencyMs           int32                    `json:"latencyMs,omitempty"`
	PBKeyLen            int32                    `json:"pbKeyLen,omitempty"`
	PassphraseSecretRef corev1.SecretKeySelector `json:"passphraseSecretRef"`
}

type RISTSpec struct {
	Profile             int32                     `json:"profile"`
	RemoteHost          string                    `json:"remoteHost"`
	RemotePort          int32                     `json:"remotePort"`
	PassphraseSecretRef *corev1.SecretKeySelector `json:"passphraseSecretRef,omitempty"`
}

type WANPathSpec struct {
	Name      string    `json:"name"`
	Transport string    `json:"transport"`
	Priority  int32     `json:"priority"`
	SRT       *SRTSpec  `json:"srt,omitempty"`
	RIST      *RISTSpec `json:"rist,omitempty"`
}

type ContributionSpec struct {
	Redundancy string        `json:"redundancy"`
	MergeMode  string        `json:"mergeMode,omitempty"`
	Paths      []WANPathSpec `json:"paths"`
}

type IngressSpec struct {
	Source       string            `json:"source,omitempty"`
	Contribution *ContributionSpec `json:"contribution,omitempty"`
}

type NAPPlacement struct {
	Name string `json:"name"`
	NAP  string `json:"nap"`
}

type ZeusRunbookSpec struct {
	EventClass string         `json:"eventClass"`
	NAPs       []NAPPlacement `json:"naps"`
	Ingress    *IngressSpec   `json:"ingress,omitempty"`
}

type PathHealth struct {
	Name      string  `json:"name"`
	Transport string  `json:"transport"`
	State     string  `json:"state"`
	RTTMs     int32   `json:"rttMs,omitempty"`
	LossPct   float64 `json:"lossPct,omitempty"`
	JitterMs  int32   `json:"jitterMs,omitempty"`
}

type ZeusRunbookStatus struct {
	Phase      string             `json:"phase,omitempty"`
	ActiveNAPs []string           `json:"activeNAPs,omitempty"`
	PathHealth []PathHealth       `json:"pathHealth,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="EventClass",type=string,JSONPath=`.spec.eventClass`
type ZeusRunbook struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ZeusRunbookSpec   `json:"spec,omitempty"`
	Status            ZeusRunbookStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ZeusRunbookList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZeusRunbook `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZeusRunbook{}, &ZeusRunbookList{})
}
