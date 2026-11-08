Zeus Broadcast Orchestrator

A neutral orchestration kernel for broadcast production on an open MXL fabric.

https://img.shields.io/badge/status-MVP%20buildable-yellow
https://img.shields.io/badge/kubernetes-1.28%2B-blue
https://img.shields.io/badge/go-1.26-blue

Table of Contents

1. What Is This
2. Quick Start
3. Architecture
4. Repository Layout
5. Source Files
6. Deployment
7. Verification Gates
8. Roadmap
9. Decision Log
10. Known Limitations

---

1. What Is This

Zeus is an orchestration kernel that:

· Accepts declarative runbooks (ZeusRunbook CRD) describing a production workflow.
· Schedules validated media applications (ZeusApplicationPackage CRD) onto Kubernetes nodes.
· Connects them over the MXL media fabric via Kubernetes CRDs (no direct libmxl-fabrics calls).
· Registers/discconects via NMOS IS-04/IS-05 (IS-10 OAuth ready).
· Enforces security invariants at admission (passphrase secrecy, transport compliance, domain isolation).

Positioning

 Grass Valley AMPP NEP Platform Zeus
Media fabric MXL (proprietary wrapper) Third-party IP flows MXL (open, mxl-k8s)
Orchestration Schema-driven Pre-validated config Declarative CRDs + webhook
Vendor neutrality GV ecosystem NEP ecosystem Open NAP schema
Agent-operable Partial No MCP-ready (Phase 2)

MVP Scope

· Single-site, tcp fabric provider
· One MXL-consuming NAP orchestrated end-to-end
· NMOS IS-04/IS-05 discovery and connection
· SRT/RIST dual-path schema accepted (runtime guard: Phase 2)

Not In MVP

· RDMA (verbs/efa) — Phase 2, pending 72-hour soak test
· Dual-path hitless merge — Phase 2
· Cross-site latency-aware scheduling — Phase 3
· Agent Plane autonomy (L1/L2) — Phase 4
· Legacy routing adapters (SW-P-08, Ember+) — Phase 3

---

2. Quick Start

Prerequisites

Tool Version Why
Go 1.26 controller-runtime v0.25 requires it
kubectl 1.28+ cluster access
helm 3.14+ OCI chart support
kind latest local cluster
Docker 20+ image builds
openssl any passphrase generation
cosign 2.x NAP signature (optional in MVP)

Materialize the Repo

```bash
# 1. Save this README as zeus-mvp.md
# 2. Run the splitter (from Part 5 below)
chmod +x tools/split-doc.sh
./tools/split-doc.sh zeus-mvp.md ./zeus

# 3. Build
cd zeus
go mod tidy
make generate manifests
make build
make test
make conversion-test
```

Deploy the MVP

```bash
# 1. Create kind cluster (kernel >= 5.17 required for mxl-k8s)
kind create cluster --image kindest/node:v1.32.0

# 2. Install mxl-k8s (TCP provider, MVP-legal)
helm install mxl oci://ghcr.io/qvest-digital/mxl-k8s/charts/mxl-k8s \
  --version 1.0.0-rc.9 \
  --namespace mxl-system --create-namespace \
  --set gateway.flags.providers='{tcp}' \
  --set gateway.rdma.enabled=false

# 3. Install Zeus CRDs and kernel
helm install zeus-crds charts/zeus-crds \
  --namespace zeus-system --create-namespace
make deploy

# 4. Deploy NMOS registry (external, optional for the gates)
kubectl apply -f deploy/nmos-registry.yaml

# 5. Create passphrase, apply runbook
./hack/gen-passphrase.sh default srt-mvp-pass
kubectl apply -f examples/vm-nap.yaml
kubectl apply -f examples/mvp-single-path.yaml

# 6. Watch it go live
kubectl get zeusrunbook mvp-single-path -w
# Reconciling -> Live

# 7. Verify fabric materialized
kubectl get mxlreceiver -n default
kubectl get mxlflowmirror -n default

# 8. Clean teardown (must leave no orphans)
kubectl delete zeusrunbook mvp-single-path
kubectl get mxlreceiver,mxlflowmirror -n default
# No resources found in default namespace.
```

If teardown leaves an MxlFlowMirror: the finalizer ordering is broken. Verify internal/fabric/real.go uses SetControllerReference (not SetOwnerReference). The mxl-k8s operator only tears down the mirror when the controller owner reference is removed.

---

3. Architecture

Three-Plane Model

```
┌─────────────────────────────────────────────────────────┐
│  Tool Plane (separate Deployments)                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐              │
│  │ MCP:     │  │ MCP:     │  │ MCP:     │              │
│  │ registry │  │ fabric   │  │ runbook  │              │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘              │
└───────┼─────────────┼─────────────┼─────────────────────┘
        │             │             │
┌───────┼─────────────┼─────────────┼─────────────────────┐
│  Control Plane (ONE binary: zeus-kernel)                │
│  ┌────────────────────────────────────────────────┐    │
│  │  ZeusApplicationPackageReconciler              │    │
│  │  ZeusRunbookReconciler  ← leader-elected       │    │
│  │  ValidatingWebhook                             │    │
│  │  internal/fabric (MxlReceiver client)          │    │
│  │  internal/nmos (IS-04/IS-05 + IS-10 OAuth)     │    │
│  └────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
        │
┌───────┼─────────────────────────────────────────────────┐
│  Media Plane (separate Deployments: NAPs)              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐              │
│  │ vision   │  │ audio    │  │ replay   │              │
│  │ mixer    │  │ console  │  │ server   │              │
│  │ (MXL)    │  │ (MXL)    │  │ (MXL)    │              │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘              │
└───────┼─────────────┼─────────────┼─────────────────────┘
        │             │             │
    ┌───┴─────────────┴─────────────┴───┐
    │  MXL Fabric (mxl-k8s control plane)│
    │  MxlDomain / MxlFlow / MxlReceiver │
    └────────────────────────────────────┘
```

Why the Kernel Is a Monolith

Reconciliation requires single-writer semantics: finalizer ordering, status ownership, and idempotency all break under decomposition. Splitting reconcilers would force a message bus and reimplement retry/ordering. Instead, scale horizontally by namespace sharding (Phase 2).

The MCP servers, Agent Plane, and NAPs are separate Deployments because they have different auth scopes, different release cadences, and different failure modes.

Data Flow (MVP)

```
1. kubectl apply -f ZeusRunbook
   ↓
2. ZeusRunbookReconciler: validate NAPs exist & Validated
   ↓
3. Check: passphrase secret exists & >= 20 chars
   ↓
4. Fabric.EnsureReceiver → creates MxlReceiver (with controller ref)
   ↓
5. mxl-k8s operator: reconciles MxlFlowMirror
   ↓
6. ensureDeployment: creates NAP Deployment (domain-scoped mount)
   ↓
7. NMOS registration (via NAP's own registration, not kernel)
   ↓
8. Status: Live
```

---

4. Repository Layout

```
zeus/
├── README.md                          # this file
├── go.mod
├── Makefile
├── .gitignore
│
├── api/v1alpha1/
│   ├── groupversion_info.go
│   ├── zeusapplicationpackage_types.go
│   ├── zeusrunbook_types.go
│   ├── zeusrunbook_webhook.go
│   └── conversion.go
│
├── internal/
│   ├── controller/
│   │   ├── zeusapplicationpackage_controller.go
│   │   ├── zeusrunbook_controller.go
│   │   └── metrics.go
│   ├── fabric/
│   │   ├── client.go
│   │   ├── fake.go
│   │   └── real.go
│   ├── nmos/
│   │   └── client.go
│   ├── transport/srt/
│   │   └── bridge.go                  # interface stub (Phase 2)
│   └── mcp/registry/
│       └── server.go                  # interface stub (Phase 2)
│
├── cmd/
│   ├── manager/main.go
│   └── zeus-nap-validate/main.go
│
├── config/                            # generated by `make manifests`
│   ├── crd/bases/
│   ├── rbac/
│   └── webhook/
│
├── charts/
│   ├── zeus-crds/Chart.yaml
│   └── zeus-kernel/
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
│           ├── deployment.yaml
│           ├── pdb.yaml
│           ├── provider-validate-daemonset.yaml
│           └── rbac.yaml
│
├── examples/
│   ├── vm-nap.yaml
│   ├── mvp-single-path.yaml
│   └── tier-one-dual-path.yaml
│
├── hack/
│   ├── gen-passphrase.sh
│   ├── rename.sh
│   └── upgrade-crds.sh
│
├── test/conversion/
│   └── roundtrip_test.go
│
└── docs/
    └── DECISION_LOG.md
```

---

5. Source Files

Extraction: Each ### File: header below is a destination path. The splitter in Part 5 uses these to materialize files.

File: tools/split-doc.sh

```bash
#!/usr/bin/env bash
# Splits a markdown file into files based on "### File: <path>" headers.
set -euo pipefail
DOC="${1:?usage: split-doc.sh <doc.md>}"
OUT="${2:-./zeus}"
mkdir -p "$OUT"
awk -v out="$OUT" '
  /^### File: / {
    file = substr($0, 11)
    gsub(/^[ \t]+|[ \t]+$/, "", file)
    path = out "/" file
    cmd = "mkdir -p \"$(dirname " path ")\""
    system(cmd)
    capture = 1
    print "" > path
    next
  }
  capture && /^```/ {
    if (inblock) { inblock = 0; capture = 0; next }
    else          { inblock = 1; next }
  }
  capture && inblock { print > path }
' "$DOC"
echo "Materialized into $OUT"
```

File: go.mod

```
module github.com/zeus-broadcast/zeus-kernel

go 1.26.0

require (
	github.com/prometheus/client_golang v1.20.5
	k8s.io/api v0.37.0
	k8s.io/apimachinery v0.37.0
	k8s.io/client-go v0.37.0
	sigs.k8s.io/controller-runtime v0.25.0
)
```

Run go mod tidy after materializing — it will populate the indirect dependencies.

File: Makefile

```makefile
IMG ?= ghcr.io/zeus-broadcast/zeus-kernel:0.1.0

LOCALBIN       := $(shell pwd)/bin
CONTROLLER_GEN := $(LOCALBIN)/controller-gen
ENVTEST        := $(LOCALBIN)/setup-envtest
ENVTEST_K8S_VERSION ?= 1.31.0

.PHONY: all
all: build

.PHONY: manifests
manifests: controller-gen
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac \
		output:webhook:artifacts:config=config/webhook

.PHONY: generate
generate: controller-gen
	$(CONTROLLER_GEN) object paths="./..."

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest
	KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" \
		go test ./... -coverprofile cover.out

.PHONY: conversion-test
conversion-test: envtest
	KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" \
		go test ./test/conversion/... -v -count=1

.PHONY: build
build: manifests generate fmt vet
	go build -o bin/manager cmd/manager/main.go
	go build -o bin/zeus-nap-validate cmd/zeus-nap-validate/main.go

.PHONY: run
run: manifests generate fmt vet
	go run ./cmd/manager/main.go

.PHONY: install
install: manifests
	kubectl apply -f config/crd/bases

.PHONY: deploy
deploy:
	helm upgrade --install zeus-kernel charts/zeus-kernel \
		--namespace zeus-system --create-namespace

.PHONY: upgrade-crds
upgrade-crds:
	./hack/upgrade-crds.sh $(ENV)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0

.PHONY: envtest
envtest: $(ENVTEST)
$(ENVTEST): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

$(LOCALBIN):
	mkdir -p $(LOCALBIN)
```

File: .gitignore

```
bin/
cover.out
*.swp
.env
```

File: api/v1alpha1/groupversion_info.go

```go
// +kubebuilder:object:generate=true
// +groupName=core.zeus.broadcast
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion  = schema.GroupVersion{Group: "core.zeus.broadcast", Version: "v1alpha1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme   = SchemeBuilder.AddToScheme
)
```

File: api/v1alpha1/zeusapplicationpackage_types.go

```go
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
	CPU          string    `json:"cpu"`
	Memory       string    `json:"memory"`
	Accelerators []string  `json:"accelerators,omitempty"`
	MediaIO      MediaIO   `json:"mediaIO,omitempty"`
}

type NMOSSpec struct {
	IS04      bool   `json:"is04,omitempty"`
	IS05      bool   `json:"is05,omitempty"`
	IS10      bool   `json:"is10,omitempty"`
	Transport string `json:"transport,omitempty"`
}

type ZeusApplicationPackageSpec struct {
	Image          string       `json:"image"`
	ImageSignature string       `json:"imageSignature"`
	CRM            CRMResources `json:"crm"`
	NMOS           NMOSSpec     `json:"nmos,omitempty"`
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
```

File: api/v1alpha1/zeusrunbook_types.go

```go
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
```

File: api/v1alpha1/conversion.go

```go
package v1alpha1

// Hub marks these types as the storage version for conversion. When v1beta1
// is introduced, its ConvertTo/ConvertFrom methods will target these types.
func (*ZeusRunbook) Hub()            {}
func (*ZeusApplicationPackage) Hub() {}
```

File: api/v1alpha1/zeusrunbook_webhook.go

```go
package v1alpha1

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var runbooklog = logf.Log.WithName("zeusrunbook-webhook")

func (r *ZeusRunbook) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		WithValidator(&ZeusRunbookValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-core-zeus-broadcast-v1alpha1-zeusrunbook,mutating=false,failurePolicy=fail,sideEffects=None,groups=core.zeus.broadcast,resources=zeusrunbooks,verbs=create;update,versions=v1alpha1,name=vzeusrunbook.kb.io,admissionReviewVersions=v1

type ZeusRunbookValidator struct{}

var _ webhook.CustomValidator = &ZeusRunbookValidator{}

func (v *ZeusRunbookValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	rb, ok := obj.(*ZeusRunbook)
	if !ok {
		return nil, fmt.Errorf("expected ZeusRunbook, got %T", obj)
	}
	return nil, validateContribution(rb)
}

func (v *ZeusRunbookValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	rb, ok := newObj.(*ZeusRunbook)
	if !ok {
		return nil, fmt.Errorf("expected ZeusRunbook, got %T", newObj)
	}
	return nil, validateContribution(rb)
}

func (v *ZeusRunbookValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func validateContribution(rb *ZeusRunbook) error {
	if rb.Spec.Ingress == nil || rb.Spec.Ingress.Contribution == nil {
		return nil
	}
	c := rb.Spec.Ingress.Contribution
	var errs field.ErrorList
	path := field.NewPath("spec", "ingress", "contribution")

	if len(c.Paths) == 0 {
		errs = append(errs, field.Required(path.Child("paths"), "at least one WAN path required"))
	}

	seen := map[string]bool{}
	for i, p := range c.Paths {
		pPath := path.Child("paths").Index(i)
		if p.Name == "" {
			errs = append(errs, field.Required(pPath.Child("name"), "path name required"))
		}
		if seen[p.Name] {
			errs = append(errs, field.Duplicate(pPath.Child("name"), p.Name))
		}
		seen[p.Name] = true

		switch p.Transport {
		case "srt":
			if p.SRT == nil {
				errs = append(errs, field.Required(pPath.Child("srt"), "srt spec required when transport=srt"))
				continue
			}
			errs = append(errs, validateSRT(p.SRT, pPath.Child("srt"))...)
			errs = append(errs, validateSecretRef(&p.SRT.PassphraseSecretRef, pPath.Child("srt", "passphraseSecretRef"))...)
		case "rist":
			if p.RIST == nil {
				errs = append(errs, field.Required(pPath.Child("rist"), "rist spec required when transport=rist"))
				continue
			}
			if p.RIST.Profile < 0 || p.RIST.Profile > 2 {
				errs = append(errs, field.Invalid(pPath.Child("rist", "profile"), p.RIST.Profile, "must be 0, 1, or 2"))
			}
			if c.MergeMode == "hitless" && p.RIST.Profile != 1 {
				errs = append(errs, field.Invalid(pPath.Child("rist", "profile"), p.RIST.Profile,
					"hitless merge requires RIST profile 1 (Main), which is ST 2022-7 capable"))
			}
			if p.RIST.PassphraseSecretRef != nil {
				errs = append(errs, validateSecretRef(p.RIST.PassphraseSecretRef, pPath.Child("rist", "passphraseSecretRef"))...)
			}
		default:
			errs = append(errs, field.NotSupported(pPath.Child("transport"), p.Transport, []string{"srt", "rist"}))
		}
	}

	if c.Redundancy == "hitless" && len(c.Paths) < 2 {
		errs = append(errs, field.Invalid(path.Child("redundancy"), c.Redundancy, "hitless merge requires at least 2 paths"))
	}

	if len(errs) > 0 {
		return apierrors.NewInvalid(GroupVersion.WithKind("ZeusRunbook").GroupKind(), rb.Name, errs)
	}
	return nil
}

func validateSRT(s *SRTSpec, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	switch s.Mode {
	case SRTCaller, SRTRendezvous:
		if s.RemoteHost == "" {
			errs = append(errs, field.Required(path.Child("remoteHost"), "required for caller/rendezvous"))
		}
		if s.RemotePort <= 0 || s.RemotePort > 65535 {
			errs = append(errs, field.Invalid(path.Child("remotePort"), s.RemotePort, "must be 1-65535"))
		}
	case SRTListener:
		if s.LocalPort <= 0 || s.LocalPort > 65535 {
			errs = append(errs, field.Invalid(path.Child("localPort"), s.LocalPort, "must be 1-65535 for listener"))
		}
	default:
		errs = append(errs, field.NotSupported(path.Child("mode"), s.Mode, []string{"caller", "listener", "rendezvous"}))
	}
	if s.PBKeyLen == 0 {
		errs = append(errs, field.Invalid(path.Child("pbKeyLen"), 0,
			"pbKeyLen is required when passphraseSecretRef is set; 0 disables AES"))
	} else if s.PBKeyLen != 16 && s.PBKeyLen != 24 && s.PBKeyLen != 32 {
		errs = append(errs, field.NotSupported(path.Child("pbKeyLen"), s.PBKeyLen, []string{"16", "24", "32"}))
	}
	return errs
}

func validateSecretRef(ref *corev1.SecretKeySelector, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	if ref.Name == "" {
		errs = append(errs, field.Required(path.Child("name"), "secret name required"))
	}
	if ref.Key == "" {
		errs = append(errs, field.Required(path.Child("key"), "secret key required"))
	}
	return errs
}
```

File: internal/controller/metrics.go

```go
package controller

import "github.com/prometheus/client_golang/prometheus"

var (
	ReconcileErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "zeus_reconcile_errors_total",
		Help: "Reconcile errors by reason",
	}, []string{"reason"})
	RunbookPhase = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zeus_runbook_phase",
		Help: "Runbooks currently in each phase",
	}, []string{"phase"})
)

func init() {
	prometheus.MustRegister(ReconcileErrors, RunbookPhase)
}
```

File: internal/controller/zeusapplicationpackage_controller.go

```go
package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

type ZeusApplicationPackageReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ZeusApplicationPackageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var nap zeusv1.ZeusApplicationPackage
	if err := r.Get(ctx, req.NamespacedName, &nap); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if nap.Status.Phase != "" {
		return ctrl.Result{}, nil
	}

	reject := func(reason string) (ctrl.Result, error) {
		nap.Status.Phase = "Rejected"
		logger.Info("nap_rejected", "nap", nap.Name, "reason", reason)
		return ctrl.Result{}, r.Status().Update(ctx, &nap)
	}

	if nap.Spec.Image == "" || nap.Spec.ImageSignature == "" {
		return reject("ImageOrSignatureMissing")
	}
	if nap.Spec.CRM.CPU == "" || nap.Spec.CRM.Memory == "" {
		return reject("CRMIncomplete")
	}
	io := nap.Spec.CRM.MediaIO
	if (io.MXLReader || io.MXLWriter) && io.DomainRef == "" {
		return reject("MXLWithoutDomainRef")
	}
	if (io.MXLReader || io.MXLWriter) && nap.Spec.NMOS.Transport != "urn:x-nmos:transport:mxl" {
		return reject("MXLWithoutMXLTransport")
	}

	nap.Status.Phase = "Validated"
	nap.Status.ObservedGen = nap.Generation
	logger.Info("nap_validated", "nap", nap.Name)
	return ctrl.Result{}, r.Status().Update(ctx, &nap)
}

func (r *ZeusApplicationPackageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&zeusv1.ZeusApplicationPackage{}).Complete(r)
}
```

File: internal/controller/zeusrunbook_controller.go

```go
package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/fabric"
)

const zeusFinalizer = "zeus.broadcast/finalizer"

type ZeusRunbookReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Fabric fabric.Client
}

func (r *ZeusRunbookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rb zeusv1.ZeusRunbook
	if err := r.Get(ctx, req.NamespacedName, &rb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !rb.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&rb, zeusFinalizer) {
			for _, p := range rb.Spec.NAPs {
				if err := r.Fabric.DeleteReceiver(ctx, rb.Namespace, p.Name); err != nil {
					logger.Error(err, "delete receiver", "nap", p.Name)
					return ctrl.Result{}, err
				}
				if err := r.deleteDeployment(ctx, rb.Namespace, p.Name); err != nil {
					return ctrl.Result{}, err
				}
			}
			controllerutil.RemoveFinalizer(&rb, zeusFinalizer)
			return ctrl.Result{}, r.Update(ctx, &rb)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&rb, zeusFinalizer) {
		controllerutil.AddFinalizer(&rb, zeusFinalizer)
		if err := r.Update(ctx, &rb); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Capability guard BEFORE any resource lookup: dual-path merge is Phase 2.
	if rb.Spec.Ingress != nil && rb.Spec.Ingress.Contribution != nil {
		c := rb.Spec.Ingress.Contribution
		if c.Redundancy == "hitless" || len(c.Paths) > 1 {
			return r.setPhase(ctx, &rb, "Degraded", "DualPathUnsupported",
				"dual-path merge is Phase 2; apply examples/mvp-single-path.yaml instead")
		}
	}

	for _, p := range rb.Spec.NAPs {
		var nap zeusv1.ZeusApplicationPackage
		if err := r.Get(ctx, types.NamespacedName{Namespace: rb.Namespace, Name: p.NAP}, &nap); err != nil {
			if apierrors.IsNotFound(err) {
				return r.setPhase(ctx, &rb, "Reconciling", "NAPNotFound", p.NAP)
			}
			return ctrl.Result{}, err
		}
		if nap.Status.Phase != "Validated" {
			return r.setPhase(ctx, &rb, "Reconciling", "NAPNotValidated", p.NAP)
		}
	}

	if c := rb.Spec.Ingress; c != nil && c.Contribution != nil {
		for _, p := range c.Contribution.Paths {
			if p.SRT == nil {
				continue
			}
			var sec corev1.Secret
			err := r.Get(ctx, types.NamespacedName{Namespace: rb.Namespace, Name: p.SRT.PassphraseSecretRef.Name}, &sec)
			if err != nil {
				if apierrors.IsNotFound(err) {
					return r.setPhase(ctx, &rb, "Degraded", "SecretNotFound", p.SRT.PassphraseSecretRef.Name)
				}
				return ctrl.Result{}, err
			}
			if len(sec.Data[p.SRT.PassphraseSecretRef.Key]) < 20 {
				return r.setPhase(ctx, &rb, "Degraded", "PassphraseTooShort",
					"passphrase must be at least 20 characters; regenerate with hack/gen-passphrase.sh")
			}
		}
	}

	for _, p := range rb.Spec.NAPs {
		flowID := rb.Name + "-" + p.Name
		if err := r.Fabric.EnsureReceiver(ctx, rb.Namespace, p.Name, flowID, p.Name, "tcp", &rb); err != nil {
			if apierrors.IsNotFound(err) {
				return r.setPhase(ctx, &rb, "Degraded", "FabricNotInstalled",
					"mxl-k8s CRDs not found; install the mxl chart")
			}
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			logger.Error(err, "fabric ensure failed", "nap", p.Name)
			return ctrl.Result{}, err
		}

		var nap zeusv1.ZeusApplicationPackage
		if err := r.Get(ctx, types.NamespacedName{Namespace: rb.Namespace, Name: p.NAP}, &nap); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureDeployment(ctx, &rb, p, &nap); err != nil {
			return r.setPhase(ctx, &rb, "Degraded", "DeployError", err.Error())
		}
	}

	return r.setPhase(ctx, &rb, "Live", "WorkflowReady", "")
}

func (r *ZeusRunbookReconciler) ensureDeployment(ctx context.Context, rb *zeusv1.ZeusRunbook, p zeusv1.NAPPlacement, nap *zeusv1.ZeusApplicationPackage) error {
	domainRef := nap.Spec.CRM.MediaIO.DomainRef
	if domainRef == "" {
		return nil
	}
	readOnly := !nap.Spec.CRM.MediaIO.MXLWriter

	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: rb.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Spec = appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": p.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": p.Name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "media",
						Image: nap.Spec.Image,
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             boolPtr(true),
							ReadOnlyRootFilesystem:   boolPtr(true),
							AllowPrivilegeEscalation: boolPtr(false),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
								Add:  []corev1.Capability{"IPC_LOCK"},
							},
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "mxl-domain",
							MountPath: "/run/mxl/domain/" + domainRef,
							ReadOnly:  readOnly,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "mxl-domain",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/run/mxl/domain",
								Type: ptrHostPathType(corev1.HostPathDirectoryOrCreate),
							},
						},
					}},
				},
			},
		}
		return controllerutil.SetControllerReference(rb, dep, r.Scheme)
	})
	return err
}

func (r *ZeusRunbookReconciler) deleteDeployment(ctx context.Context, namespace, name string) error {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	err := r.Delete(ctx, dep)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (r *ZeusRunbookReconciler) setPhase(ctx context.Context, rb *zeusv1.ZeusRunbook, phase, reason, msg string) (ctrl.Result, error) {
	if rb.Status.Phase == phase && len(rb.Status.Conditions) > 0 && rb.Status.Conditions[0].Reason == reason {
		return ctrl.Result{}, nil
	}
	rb.Status.Phase = phase
	rb.Status.Conditions = []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: metav1.Now(),
	}}
	log.FromContext(ctx).Info("phase_transition",
		"runbook", rb.Name, "phase", phase, "reason", reason,
		"actor", "controller", "causal_chain_id", string(rb.UID))
	RunbookPhase.WithLabelValues(phase).Set(1)
	if err := r.Status().Update(ctx, rb); err != nil {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}
	return ctrl.Result{}, nil
}

func (r *ZeusRunbookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&zeusv1.ZeusRunbook{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

func int32Ptr(i int32) *int32                                  { return &i }
func boolPtr(b bool) *bool                                     { return &b }
func ptrHostPathType(t corev1.HostPathType) *corev1.HostPathType { return &t }
```

File: internal/fabric/client.go

```go
package fabric

import (
	"context"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

type Client interface {
	EnsureReceiver(ctx context.Context, namespace, name, flowID, podName, provider string, owner *zeusv1.ZeusRunbook) error
	DeleteReceiver(ctx context.Context, namespace, name string) error
}
```

File: internal/fabric/fake.go

```go
package fabric

import (
	"context"
	"sync"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

type FakeClient struct {
	mu        sync.Mutex
	receivers map[string]string
}

func NewFakeClient() *FakeClient { return &FakeClient{receivers: map[string]string{}} }

func (f *FakeClient) EnsureReceiver(_ context.Context, namespace, name, flowID, _, _ string, _ *zeusv1.ZeusRunbook) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receivers[namespace+"/"+name] = flowID
	return nil
}

func (f *FakeClient) DeleteReceiver(_ context.Context, namespace, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.receivers, namespace+"/"+name)
	return nil
}

func (f *FakeClient) Get(namespace, name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.receivers[namespace+"/"+name]
	return v, ok
}
```

File: internal/fabric/real.go

```go
package fabric

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

var (
	mxlReceiverGVK         = schema.GroupVersionKind{Group: "mxl.qvest-digital.com", Version: "v1alpha1", Kind: "MxlReceiver"}
	mxlNodeCapabilitiesGVK = schema.GroupVersionKind{Group: "mxl.qvest-digital.com", Version: "v1alpha1", Kind: "MxlNodeCapabilities"}
)

type RealClient struct{ c client.Client }

func NewRealClient(c client.Client) *RealClient { return &RealClient{c: c} }

func (r *RealClient) EnsureReceiver(ctx context.Context, namespace, name, flowID, podName, provider string, owner *zeusv1.ZeusRunbook) error {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(mxlReceiverGVK)
	if err := r.c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existing); err == nil {
		return nil
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(mxlReceiverGVK)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	if err := controllerutil.SetControllerReference(owner, obj, r.c.Scheme()); err != nil {
		return fmt.Errorf("set controller ref: %w", err)
	}
	obj.Object["spec"] = map[string]interface{}{
		"flowID":   flowID,
		"provider": provider,
		"podRef":   map[string]interface{}{"name": podName, "namespace": namespace},
	}
	return r.c.Create(ctx, obj)
}

func (r *RealClient) DeleteReceiver(ctx context.Context, namespace, name string) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(mxlReceiverGVK)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	if err := r.c.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("delete mxlreceiver: %w", err)
	}
	return nil
}

func RequireProvider(ctx context.Context, c client.Client, nodeName, provider string) error {
	nc := &unstructured.Unstructured{}
	nc.SetGroupVersionKind(mxlNodeCapabilitiesGVK)
	if err := c.Get(ctx, client.ObjectKey{Name: nodeName}, nc); err != nil {
		return fmt.Errorf("node %s has no MxlNodeCapabilities: %w", nodeName, err)
	}
	providers, _, _ := unstructured.NestedStringSlice(nc.Object, "status", "providers")
	for _, p := range providers {
		if p == provider {
			return nil
		}
	}
	return fmt.Errorf("node %s does not advertise provider %q (has %v)", nodeName, provider, providers)
}
```

File: internal/nmos/client.go

```go
package nmos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Client struct {
	BaseURL      string
	TokenURL     string
	ClientID     string
	ClientSecret string
	HTTP         *http.Client

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (c *Client) tokenFor(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiresAt.Add(-30*time.Second)) {
		return c.token, nil
	}
	body, _ := json.Marshal(map[string]string{
		"grant_type": "client_credentials", "client_id": c.ClientID, "client_secret": c.ClientSecret,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint %d: %s", resp.StatusCode, b)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	c.token = tok.AccessToken
	c.expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return c.token, nil
}

func (c *Client) PatchIS05(ctx context.Context, receiverID string, body []byte) error {
	tok, err := c.tokenFor(ctx)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/x-nmos/connection/v1.1/single/receivers/%s/staged", c.BaseURL, receiverID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("IS-05 PATCH %d: %s", resp.StatusCode, b)
	}
	return nil
}
```

File: internal/transport/srt/bridge.go

```go
package srt

import (
	"context"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

// Bridge is the WAN-facing SRT leg. The MVP uses a single caller; Phase 2 adds
// listener, rendezvous, and dual-path hitless merge. The passphrase must be
// resolved from the referenced Secret BEFORE Start; the Bridge never reads
// Secrets directly.
type Bridge interface {
	Start(ctx context.Context, spec zeusv1.SRTSpec, passphrase []byte) error
	Health(ctx context.Context) (zeusv1.PathHealth, error)
	Stop(ctx context.Context) error
}
```

File: internal/mcp/registry/server.go

```go
package registry

import (
	"context"
)

// RegistryMCP exposes IS-04 query and IS-05 connect/disconnect as MCP tools.
// Authorization is enforced IN CODE before any request leaves the server.
type RegistryMCP interface {
	QueryReceivers(ctx context.Context) ([]map[string]interface{}, error)
	Connect(ctx context.Context, senderID, receiverID string) error
	Disconnect(ctx context.Context, receiverID string) error
}
```

File: cmd/manager/main.go

```go
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
	"github.com/zeus-broadcast/zeus-kernel/internal/controller"
	"github.com/zeus-broadcast/zeus-kernel/internal/fabric"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(zeusv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr, probeAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "zeus.broadcast",
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.ZeusApplicationPackageReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create NAP controller")
		os.Exit(1)
	}

	if err := (&controller.ZeusRunbookReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
		Fabric: fabric.NewRealClient(mgr.GetClient()),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create Runbook controller")
		os.Exit(1)
	}

	if err := (&zeusv1.ZeusRunbook{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
```

File: cmd/zeus-nap-validate/main.go

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: zeus-nap-validate <nap.yaml> [pub-key-path]")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail("read: %v", err)
	}

	var nap zeusv1.ZeusApplicationPackage
	if err := json.Unmarshal(data, &nap); err != nil {
		fail("parse: %v", err)
	}

	if nap.Spec.Image == "" || nap.Spec.ImageSignature == "" {
		fail("image and imageSignature are required")
	}
	if nap.Spec.CRM.CPU == "" || nap.Spec.CRM.Memory == "" {
		fail("crm.cpu and crm.memory are required")
	}
	io := nap.Spec.CRM.MediaIO
	if (io.MXLReader || io.MXLWriter) && io.DomainRef == "" {
		fail("MXL NAPs must declare crm.mediaIO.domainRef")
	}
	if (io.MXLReader || io.MXLWriter) && nap.Spec.NMOS.Transport != "urn:x-nmos:transport:mxl" {
		fail("MXL NAPs must declare nmos.transport: urn:x-nmos:transport:mxl (BCP-007-03)")
	}
	if len(os.Args) >= 3 {
		cmd := exec.Command("cosign", "verify", "--key", os.Args[2], nap.Spec.Image)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fail("cosign verify failed: %v", err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "WARN: no public key supplied; skipping cosign verification")
	}
	fmt.Printf("PASS: %s (%s)\n", nap.Name, nap.Spec.Image)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
```

File: examples/vm-nap.yaml

```yaml
apiVersion: core.zeus.broadcast/v1alpha1
kind: ZeusApplicationPackage
metadata:
  name: vision-mixer-1
  namespace: default
spec:
  image: registry.local/vm:test
  imageSignature: "cosign:placeholder"
  crm:
    cpu: "16"
    memory: "32Gi"
    mediaIO:
      mxlReader: true
      mxlWriter: true
      domainRef: "domain-a"
      formats: ["video/v210"]
  nmos:
    is04: true
    is05: true
    transport: urn:x-nmos:transport:mxl
  healthPath: /healthz
```

File: examples/mvp-single-path.yaml

```yaml
apiVersion: core.zeus.broadcast/v1alpha1
kind: ZeusRunbook
metadata:
  name: mvp-single-path
  namespace: default
spec:
  eventClass: mvp-proof
  naps:
    - name: vision-mixer-1
      nap: vision-mixer-1
  ingress:
    contribution:
      redundancy: none
      mergeMode: switchover
      paths:
        - name: primary
          transport: srt
          priority: 1
          srt:
            mode: caller
            remoteHost: 203.0.113.10
            remotePort: 9000
            latencyMs: 120
            pbKeyLen: 32
            passphraseSecretRef:
              name: srt-mvp-pass
              key: passphrase
```

File: examples/tier-one-dual-path.yaml

```yaml
apiVersion: core.zeus.broadcast/v1alpha1
kind: ZeusRunbook
metadata:
  name: tier-one-dual-path
  namespace: default
spec:
  eventClass: tier-one-football
  naps:
    - name: vision-mixer-1
      nap: vision-mixer-1
  ingress:
    contribution:
      redundancy: hitless
      mergeMode: hitless
      paths:
        - name: srt-primary
          transport: srt
          priority: 1
          srt:
            mode: rendezvous
            remoteHost: 198.51.100.20
            remotePort: 9000
            latencyMs: 80
            pbKeyLen: 32
            passphraseSecretRef:
              name: srt-tier1-pass
              key: passphrase
        - name: rist-backup
          transport: rist
          priority: 2
          rist:
            profile: 1
            remoteHost: 198.51.100.21
            remotePort: 9100
            passphraseSecretRef:
              name: rist-tier1-pass
              key: passphrase
```

File: hack/gen-passphrase.sh

```bash
#!/usr/bin/env bash
set -euo pipefail
NS="${1:?usage: gen-passphrase.sh <namespace> <secret-name>}"
NAME="${2:?usage: gen-passphrase.sh <namespace> <secret-name>}"

PASS=$(openssl rand -base64 24)
kubectl create secret generic "$NAME" \
  --from-literal=passphrase="$PASS" \
  --namespace "$NS" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "Secret $NS/$NAME created (${#PASS} chars)."
```

File: hack/rename.sh

```bash
#!/usr/bin/env bash
set -euo pipefail
OLD_NAME="zeus"
NEW_NAME="${1:?usage: rename.sh <new-name>}"

grep -rl --exclude-dir=.git --exclude-dir=bin \
  -e "zeus-broadcast" -e "zeus\.broadcast" -e "zeus-kernel" -e "zeus-nap-validate" -e "zeus-crds" . \
  | xargs sed -i \
    -e "s|github.com/zeus-broadcast/${OLD_NAME}-kernel|github.com/${NEW_NAME}-broadcast/${NEW_NAME}-kernel|g" \
    -e "s|${OLD_NAME}\.broadcast|${NEW_NAME}.broadcast|g" \
    -e "s|${OLD_NAME}-kernel|${NEW_NAME}-kernel|g" \
    -e "s|${OLD_NAME}-crds|${NEW_NAME}-crds|g" \
    -e "s|${OLD_NAME}-nap-validate|${NEW_NAME}-nap-validate|g" \
    -e "s|ghcr.io/zeus-broadcast|ghcr.io/${NEW_NAME}-broadcast|g"

echo "Renamed ${OLD_NAME} -> ${NEW_NAME}. Run 'make generate manifests'."
```

File: hack/upgrade-crds.sh

```bash
#!/usr/bin/env bash
set -euo pipefail
ENV="${1:?usage: upgrade-crds.sh <env>}"
GEN=$(mktemp -d)
trap 'rm -rf "$GEN"' EXIT

helm template zeus-crds charts/zeus-crds --include-crds > "$GEN/all.yaml"

for crd in $(yq eval 'select(.kind == "CustomResourceDefinition") | .metadata.name' "$GEN/all.yaml"); do
  echo "Checking $crd"
  kubectl get crd "$crd" -o json 2>/dev/null \
    | jq -S '[.spec.versions[] | {name, served, storage, schema}]' > "$GEN/installed-$crd.json" || true
  yq eval "select(.kind == \"CustomResourceDefinition\" and .metadata.name == \"$crd\") | [.spec.versions[] | {name, served, storage, schema}]" \
    "$GEN/all.yaml" | jq -S . > "$GEN/new-$crd.json"

  if [[ -s "$GEN/installed-$crd.json" ]] && ! diff -q "$GEN/installed-$crd.json" "$GEN/new-$crd.json" >/dev/null; then
    jq -r '.[].schema.openAPIV3Schema | paths(scalars) as $p | $p | join(".")' "$GEN/installed-$crd.json" | sort -u > "$GEN/old-paths"
    jq -r '.[].schema.openAPIV3Schema | paths(scalars) as $p | $p | join(".")' "$GEN/new-$crd.json"       | sort -u > "$GEN/new-paths"
    if comm -23 "$GEN/old-paths" "$GEN/new-paths" | grep -q .; then
      echo "ERROR: $crd removes fields. Add a conversion webhook." >&2
      comm -23 "$GEN/old-paths" "$GEN/new-paths" >&2
      exit 1
    fi
  fi
done

kubectl apply -f "$GEN/all.yaml"
echo "CRDs applied."
```

File: test/conversion/roundtrip_test.go

```go
package conversion_test

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	zeusv1 "github.com/zeus-broadcast/zeus-kernel/api/v1alpha1"
)

func TestRunbookRoundTrip(t *testing.T) {
	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer env.Stop()

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = zeusv1.AddToScheme(scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	in := &zeusv1.ZeusRunbook{
		ObjectMeta: metav1.ObjectMeta{Name: "rt", Namespace: "default"},
		Spec: zeusv1.ZeusRunbookSpec{
			EventClass: "rt",
			NAPs:       []zeusv1.NAPPlacement{{Name: "vm", NAP: "vm"}},
		},
	}
	ctx := context.Background()
	if err := c.Create(ctx, in); err != nil {
		t.Fatalf("create: %v", err)
	}
	out := &zeusv1.ZeusRunbook{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(in), out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !reflect.DeepEqual(in.Spec, out.Spec) {
		t.Fatalf("spec drift: in=%+v out=%+v", in.Spec, out.Spec)
	}
}
```

File: charts/zeus-crds/Chart.yaml

```yaml
apiVersion: v2
name: zeus-crds
description: Zeus CRDs (versioned independently)
type: application
version: 1.0.0
appVersion: "1.0.0"
```

The crds/ subdirectory is populated by make manifests — copy config/crd/bases/*.yaml into charts/zeus-crds/crds/ before packaging.

File: charts/zeus-kernel/Chart.yaml

```yaml
apiVersion: v2
name: zeus-kernel
description: Zeus Broadcast Orchestrator control-plane kernel
type: application
version: 0.1.0
appVersion: "0.1.0"
```

File: charts/zeus-kernel/values.yaml

```yaml
replicaCount: 2

image:
  repository: ghcr.io/zeus-broadcast/zeus-kernel
  tag: "0.1.0"
  pullPolicy: IfNotPresent

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi

topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: DoNotSchedule
    labelSelector:
      matchLabels:
        app: zeus-kernel

nmos:
  registryURL: ""
  authz:
    enabled: false
    tokenURL: ""
    clientID: ""
    clientSecretRef:
      name: ""
      key: ""

mxl:
  apiGroup: "mxl.qvest-digital.com"
  defaultProvider: "tcp"

leaderElection:
  enabled: true
```

File: charts/zeus-kernel/templates/deployment.yaml

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: zeus-kernel
  namespace: {{ .Release.Namespace }}
  labels:
    app: zeus-kernel
spec:
  replicas: {{ .Values.replicaCount }}
  selector:
    matchLabels:
      app: zeus-kernel
  template:
    metadata:
      labels:
        app: zeus-kernel
    spec:
      serviceAccountName: zeus-kernel
      topologySpreadConstraints:
{{ toYaml .Values.topologySpreadConstraints | indent 8 }}
      containers:
        - name: manager
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          args:
            - --leader-elect={{ .Values.leaderElection.enabled }}
            - --health-probe-bind-address=:8081
            - --metrics-bind-address=:8080
          resources:
{{ toYaml .Values.resources | indent 12 }}
          livenessProbe:
            httpGet: { path: /healthz, port: 8081 }
            initialDelaySeconds: 15
          readinessProbe:
            httpGet: { path: /readyz, port: 8081 }
            initialDelaySeconds: 5
          securityContext:
            runAsNonRoot: true
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
          ports:
            - { name: webhook, containerPort: 9443 }
            - { name: metrics, containerPort: 8080 }
          volumeMounts:
            - { name: tmp, mountPath: /tmp }
      volumes:
        - { name: tmp, emptyDir: {} }
```

File: charts/zeus-kernel/templates/pdb.yaml

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: zeus-kernel
  namespace: {{ .Release.Namespace }}
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: zeus-kernel
```

File: charts/zeus-kernel/templates/provider-validate-daemonset.yaml

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: zeus-provider-validate
  namespace: {{ .Release.Namespace }}
  labels:
    app: zeus-provider-validate
spec:
  selector:
    matchLabels:
      app: zeus-provider-validate
  template:
    metadata:
      labels:
        app: zeus-provider-validate
    spec:
      hostNetwork: true
      tolerations:
        - operator: Exists
      containers:
        - name: validate
          image: "{{ .Values.image.repository }}-provider-validate:{{ .Values.image.tag }}"
          env:
            - name: REQUIRED_PROVIDER
              value: {{ .Values.mxl.defaultProvider | quote }}
          securityContext:
            capabilities:
              add: ["IPC_LOCK"]
          volumeMounts:
            - { name: dev-infiniband, mountPath: /dev/infiniband, readOnly: true }
            - { name: mxl-domain, mountPath: /run/mxl/domain }
      volumes:
        - { name: dev-infiniband, hostPath: { path: /dev/infiniband, type: DirectoryOrCreate } }
        - { name: mxl-domain, hostPath: { path: /run/mxl/domain, type: DirectoryOrCreate } }
```

File: charts/zeus-kernel/templates/rbac.yaml

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: zeus-kernel
  namespace: {{ .Release.Namespace }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: zeus-kernel
rules:
  - apiGroups: ["core.zeus.broadcast"]
    resources: ["zeusrunbooks", "zeusapplicationpackages"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["core.zeus.broadcast"]
    resources: ["zeusrunbooks/status", "zeusapplicationpackages/status"]
    verbs: ["get", "update", "patch"]
  - apiGroups: ["mxl.qvest-digital.com"]
    resources: ["mxlreceivers", "mxlflows", "mxldomains", "mxlflowmirrors", "mxlnodecapabilities"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: zeus-kernel
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: zeus-kernel
subjects:
  - kind: ServiceAccount
    name: zeus-kernel
    namespace: {{ .Release.Namespace }}
```

File: docs/DECISION_LOG.md

```markdown
# Zeus — Decision Log

## D-001: Product name
"Zeus" with API group `zeus.broadcast`, module `github.com/zeus-broadcast/zeus-kernel`.
**Risk:** Heavy namespace collision. All identifiers derive from `hack/rename.sh`; a rename
after Milestone 1 requires a CRD migration plan. Revisit before external NAP publication.

## D-002: MXL CRD API group
Pin to `mxl.qvest-digital.com/v1alpha1`, access via `unstructured.Unstructured`. The kernel
isolates the string in `internal/fabric/real.go`.

## D-003: WAN passphrase handling
SRT/RIST passphrases are `corev1.SecretKeySelector` only. No plaintext field. Webhook rejects
empty name/key; reconciler rejects secrets under 20 chars.

## D-004: Dual-path merge deferred to Phase 2
Schema accepts `redundancy: hitless` today; MVP reconciler returns
`Degraded/DualPathUnsupported` at runtime. **Not rejected by webhook** so Phase 2 is an
implementation change, not a CRD bump.

## D-005: NMOS registry is external
Kernel consumes IS-04 and issues IS-05; it does not host a registry. Use `nmos-cpp` or
`easy-nmos` in development.

## D-006: PathHealth in status
`ZeusRunbookStatus.PathHealth` is first-class. Agent Plane cannot remediate what it cannot read.

## D-007: RDMA provider deferred
MVP targets `tcp` only. `verbs`/`efa` are Phase 2 with a 72-hour soak test. mxl-k8s routine
CI exercises `tcp`; RDMA paths are API-accepted but not CI-tested upstream.

## D-008: Microservice boundary
Kernel is one binary. MCP servers, agents, and NAPs are separate Deployments. Splitting
reconcilers breaks finalizer atomicity and status ownership.
```

---

6. Deployment

Production Profile

The MVP is deployable. The production profile requires these values in charts/zeus-kernel/values.yaml:

```yaml
nmos:
  authz:
    enabled: true                    # IS-10 required in production
    tokenURL: "https://authz.facility.local/oauth/token"
    clientID: "zeus-kernel"
    clientSecretRef:
      name: zeus-nmos-credentials
      key: client-secret

mxl:
  defaultProvider: "tcp"             # flip to "verbs" only after soak test
```

Create the NMOS credentials secret:

```bash
kubectl create secret generic zeus-nmos-credentials \
  --from-literal=client-secret='<from-authz-server>' \
  -n zeus-system
```

Upgrade Path

```bash
# 1. Upgrade CRDs first (never bundled with the kernel chart)
make upgrade-crds ENV=prod

# 2. Verify admission with a dry-run before applying to production
kubectl apply --dry-run=server -f examples/mvp-single-path.yaml

# 3. Upgrade kernel
helm upgrade zeus-kernel charts/zeus-kernel -n zeus-system

# 4. Watch for phase regressions
kubectl get zeusrunbooks -A -w
```

If the upgrade breaks admission: temporarily set failurePolicy: Ignore on the validating webhook, roll back, and fix forward. Never remove the webhook entirely.

---

7. Verification Gates

Run in order. A passing build does not prove production readiness — these gates do.

# Gate Command Pass
1 Build make build bin/manager, bin/zeus-nap-validate exist
2 Unit + envtest make test All green
3 Conversion round-trip make conversion-test Spec unchanged after Create/Get
4 Webhook accepts MVP kubectl apply --dry-run=server -f examples/mvp-single-path.yaml Accepted
5 Dual-path forward-compat kubectl apply --dry-run=server -f examples/tier-one-dual-path.yaml Accepted (runtime guard, not admission)
6 Invalid SRT rejected apply runbook with mode: caller no remoteHost Required value from webhook
7 Passphrase secrecy kubectl get zeusrunbook -o yaml \| grep -i passphrase Only name/key
8 End-to-end kubectl apply -f examples/mvp-single-path.yaml Live, PathHealth[0].state: up
9 Clean teardown kubectl delete zeusrunbook mvp-single-path No MxlReceiver/MxlFlowMirror
10 Metrics curl :8080/metrics \| grep zeus_ zeus_reconcile_errors_total, zeus_runbook_phase present
11 Audit log kubectl logs -n zeus-system deploy/zeus-kernel \| grep phase_transition JSON lines with causal_chain_id
12 Node drain resilience kubectl drain <node> One replica stays up

Gates 1–3 must be run before claiming the code is buildable. Gates 4–12 must be run before claiming the product is deployable.

---

8. Roadmap

Phase 1 — MVP (Weeks 1–6) ✅ Buildable

☑ CRD types, webhook, reconcilers
☑ Fabric client (fake + real)
☑ NMOS OAuth client (IS-10)
☑ Helm charts, PDB, topology spread
☑ Metrics, audit log, conversion scaffolding
☐ End-to-end verification on real kind cluster

Phase 2 — Hardening (Weeks 7–12)

☐ SRT bridge implementation (caller/listener/rendezvous + AES)
☐ RIST bridge implementation (profile 1, 2022-7 merge)
☐ Dual-path hitless merge (remove DualPathUnsupported guard)
☐ RDMA verbs provider: 72-hour soak test, PFC validation per node
☐ MCP servers: registry, fabric, runbook as separate Deployments
☐ Cross-site scheduler: latency probe + cost model
☐ Agent Plane v1: L0 suggest only

Phase 3 — Ecosystem (Weeks 13–18)

☐ NAP conformance suite published as CLI + CI action
☐ Self-service NAP registry (staging + production)
☐ Runbook template library (event classes)
☐ 15+ NAP-certified applications
☐ Canonical/legacy routing adapter (SW-P-08, Ember+)
☐ Device control adapters (camera, audio, replay, graphics)

Phase 4 — Agent Plane (Weeks 19–24)

☐ L1 pre-authorized execution with causal-chain logging
☐ Orchestration remediation (degradation management)
☐ Independent entity governance structure

Phase 5+ — Standardization

☐ L2 supervised autonomy (after 50 zero-rollback L1 executions)
☐ Outcome-based pricing pilot
☐ NAP schema + runbook spec submitted to AMWA/DMF

---

9. Decision Log

See docs/DECISION_LOG.md above. Key decisions:

ID Decision Risk Revisit
D-001 Name "Zeus" Heavy collision risk Before external NAPs
D-002 MXL group mxl.qvest-digital.com Upstream group may change On mxl-k8s major bump
D-003 SecretKeySelector only for passphrases None — enforced by construction Never
D-004 Dual-path deferred to Phase 2 Runtime guard, not admission Phase 2
D-005 External NMOS registry None — avoids second source of truth Never
D-006 PathHealth in status None — required for Agent Plane Never
D-007 RDMA deferred to Phase 2 CI gap upstream After 72h soak
D-008 Monolith kernel None — required for finalizer atomicity Never

---

10. Known Limitations

Blockers for General Availability

1. RDMA is not CI-tested. The verbs/efa paths in mxl-k8s are API-accepted but not exercised in routine CI. Do not claim production readiness on RDMA until the 72-hour soak test passes. The MVP uses tcp only.
2. Dual-path merge is a runtime guard, not an implementation. The schema accepts redundancy: hitless; the reconciler returns Degraded/DualPathUnsupported. Apply only examples/mvp-single-path.yaml in production until Phase 2.
3. NMOS IS-10 is scaffolded, not verified. The OAuth client compiles and has the correct flow, but has not been tested against a real IS-10-enabled registry. Verify against your facility's Authorization Server in a lab before enabling nmos.authz.enabled=true.
4. MxlNodeCapabilities provider probing is read-only. The RequireProvider function reads the CRD but is not called by the MVP reconciler. Add the call before allowing non-tcp providers.

Not In This Repo

· SRT bridge implementation — interface stub only
· RIST bridge implementation — not stubbed
· MCP servers — interface stub only
· Agent Plane — Phase 4
· Legacy routing adapters — Phase 3
· Cross-site scheduler — Phase 2
· Media exchange layer (MXL is external, via mxl-k8s chart)

Naming

Zeus has significant namespace collision in software (Zeus Technology, Zeus Web Server, Zeus CMS, etc.). The hack/rename.sh script is built for a clean rename; recommended fallbacks from earlier analysis: Weft (weft.broadcast), Heddle, Genlock, Downbeat. Execute the rename before publishing any external NAPs, and before make upgrade-crds finalizes the CRD version.

---

License

Apache 2.0 — see LICENSE (add before publishing).

Contributing

Before opening a PR:

1. Run all 12 verification gates above.
2. Add a decision log entry for any API change.
3. Do not remove fields from v1alpha1 — add v1beta1 with a conversion webhook.

---

End of README.
This document is the complete MVP specification. Save as README.md, run ./tools/split-doc.sh README.md, and make build to start development.