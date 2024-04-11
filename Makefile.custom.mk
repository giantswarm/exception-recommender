##@ Build Dependencies
ENVTEST_K8S_VERSION = 1.27.1
GOBIN=$(shell go env GOPATH)/bin

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.10.0)

ENVTEST = $(shell pwd)/bin/setup-envtest
.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	@echo "====> $@"
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

GINKGO = $(shell pwd)/bin/ginkgo
.PHONY: ginkgo
ginkgo: ## Download ginkgo locally if necessary.
	@echo "====> $@"
	$(call go-get-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo@latest)

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

test-unit: ginkgo envtest  ## Run unit tests
	@echo "====> $@"
	f="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" $(GINKGO) -p --nodes 4 -r -randomize-all --randomize-suites --skip-package=tests --cover --coverpkg=`go list ./... | grep -v fakes | tr '\n' ','` ./...
