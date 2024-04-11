##@ Build Dependencies
ENVTEST_K8S_VERSION = 1.27.1
GOBIN=$(shell go env GOPATH)/bin

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

ENVTEST ?= $(LOCALBIN)/setup-envtest

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.10.0)

ENVTEST = $(shell pwd)/bin/setup-envtest
.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

GINKGO = $(shell pwd)/bin/ginkgo
.PHONY: ginkgo
ginkgo: ## Download ginkgo locally if necessary.
	$(call go-get-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo@latest)

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

test-unit: ginkgo fmt vet envtest ## Run unit tests
	@echo "====> $@"
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -ldflags "$(LDFLAGS)" -race ./...
