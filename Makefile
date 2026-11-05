
IMG ?= ghcr.io/zeus-broadcast/zeus-kernel:0.1.0

LOCALBIN       := $(shell pwd)/bin
CONTROLLER_GEN := $(LOCALBIN)/controller-gen
ENVTEST        := $(LOCALBIN)/setup-envtest
ENVTEST_K8S_VERSION ?= 1.31.0

# Prefer the local envtest asset store. `setup-envtest use` refreshes its
# release index from GitHub on every invocation, which fails (TLS handshake
# timeout) on restricted networks even when the assets are already installed.
ENVTEST_ASSETS = $(if $(wildcard $(HOME)/.local/share/kubebuilder-envtest/k8s/$(ENVTEST_K8S_VERSION)-linux-amd64),$(wildcard $(HOME)/.local/share/kubebuilder-envtest/k8s/$(ENVTEST_K8S_VERSION)-linux-amd64),$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path 2>/dev/null))

.PHONY: all
all: build

.PHONY: manifests
manifests: controller-gen
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd:allowDangerousTypes=true webhook \
		paths="./api/v1alpha1" paths="./internal/controller" paths="./internal/fabric" \
		paths="./internal/nmos" paths="./internal/transport/srt" paths="./internal/mcp/registry" \
		paths="./cmd/manager" paths="./cmd/zeus-nap-validate" \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac \
		output:webhook:artifacts:config=config/webhook

.PHONY: generate
generate: controller-gen
	$(CONTROLLER_GEN) object paths="./api/v1alpha1"

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest
	KUBEBUILDER_ASSETS="$(ENVTEST_ASSETS)" \
		go test ./... -coverprofile cover.out

.PHONY: conversion-test
conversion-test: manifests envtest
	KUBEBUILDER_ASSETS="$(ENVTEST_ASSETS)" \
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
