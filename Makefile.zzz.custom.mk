##@ Build Dependencies
ENVTEST_K8S_VERSION = 1.27.1
GOBIN=$(shell go env GOPATH)/bin

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

ENVTEST ?= $(LOCALBIN)/setup-envtest

test: ## Runs go test with default values.
	@echo "====> $@"
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -ldflags "$(LDFLAGS)" -race ./...

$(SOURCES): test
