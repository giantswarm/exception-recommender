# Image URL to use all building/pushing image targets
IMG ?= gsoci.azurecr.io/giantswarm/aws-resolver-rules-operator:dev

# Substitute colon with space - this creates a list.
# Word selects the n-th element of the list
IMAGE_REPO = $(word 1,$(subst :, ,$(IMG)))
IMAGE_TAG = $(word 2,$(subst :, ,$(IMG)))

CLUSTER ?= acceptance
MANAGEMENT_CLUSTER_NAME ?= test-mc
MANAGEMENT_CLUSTER_NAMESPACE ?= test

DOCKER_COMPOSE = bin/docker-compose

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	go generate ./...
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: create-acceptance-cluster
create-acceptance-cluster: kind
	KIND=$(KIND) CLUSTER=$(CLUSTER) IMG=$(IMG) MANAGEMENT_CLUSTER_NAMESPACE=$(MANAGEMENT_CLUSTER_NAMESPACE) ./scripts/ensure-kind-cluster.sh

.PHONY: install-cluster-api
install-cluster-api: clusterctl
	AWS_B64ENCODED_CREDENTIALS="" GOPROXY="off" $(CLUSTERCTL) init --kubeconfig "$(KUBECONFIG)" --infrastructure=aws:v2.3.0 --wait-providers || true

.PHONY: deploy-acceptance-cluster
deploy-acceptance-cluster: docker-build create-acceptance-cluster install-cluster-api deploy

.PHONY: test-unit
test-unit: ginkgo generate fmt vet envtest ## Run unit tests
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" $(GINKGO) -p --nodes 4 -r -randomize-all --randomize-suites --skip-package=tests --cover --coverpkg=`go list ./... | grep -v fakes | tr '\n' ','` ./...

.PHONY: test-integration
test-integration: test-integration-localstack test-integration-aws

.PHONY: test-integration-localstack
test-integration-localstack: ginkgo start-localstack ## Run integration tests against localstack
	AWS_ACCESS_KEY_ID="dummy" AWS_SECRET_ACCESS_KEY="dummy" AWS_ENDPOINT="http://localhost:4566" AWS_REGION="eu-central-1" $(GINKGO) -p --nodes 4 -r -randomize-all --randomize-suites --cover --coverpkg=github.com/aws-resolver-rules-operator/pkg/aws tests/integration/localstack
	$(MAKE) stop-localstack

.PHONY: test-integration-aws
test-integration-aws: ginkgo ## Run integration tests against aws
	$(GINKGO) -p --nodes 4 -r -randomize-all -v --randomize-suites --cover --coverpkg=github.com/aws-resolver-rules-operator/pkg/aws tests/integration/aws
	$(MAKE) stop-localstack

.PHONY: run-acceptance-tests
run-acceptance-tests: KUBECONFIG=$(HOME)/.kube/$(CLUSTER).yml
run-acceptance-tests:
	KUBECONFIG="$(KUBECONFIG)" \
	MANAGEMENT_CLUSTER_NAME="$(MANAGEMENT_CLUSTER_NAME)" \
	MANAGEMENT_CLUSTER_NAMESPACE="$(MANAGEMENT_CLUSTER_NAMESPACE)" \
	$(GINKGO) -r -randomize-all --randomize-suites tests/acceptance

.PHONY: test-acceptance
test-acceptance: KUBECONFIG=$(HOME)/.kube/$(CLUSTER).yml
test-acceptance: ginkgo deploy-acceptance-cluster run-acceptance-tests## Run acceptance testst

.PHONY: ensure-deploy-envs
ensure-deploy-envs:
ifndef AWS_ACCESS_KEY_ID
	$(error AWS_ACCESS_KEY_ID is undefined)
endif
ifndef AWS_SECRET_ACCESS_KEY
	$(error AWS_SECRET_ACCESS_KEY is undefined)
endif
ifndef AWS_REGION
	$(error AWS_REGION is undefined)
endif


.PHONY: deploy
deploy: ensure-deploy-envs ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	KUBECONFIG=$(KUBECONFIG) helm upgrade --install \
		--namespace giantswarm \
		--set image.tag=$(IMAGE_TAG) \
		--set disableResolverControllers=true \
		--set managementClusterName=$(MANAGEMENT_CLUSTER_NAME) \
		--set managementClusterNamespace=$(MANAGEMENT_CLUSTER_NAMESPACE) \
		--set aws.accessKeyID=$(AWS_ACCESS_KEY_ID) \
		--set aws.secretAccessKey=$(AWS_SECRET_ACCESS_KEY) \
		--set aws.region=$(AWS_REGION) \
		--set global.podSecurityStandards.enforced=true \
		--wait \
		aws-resolver-rules-operator helm/aws-resolver-rules-operator

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s  specified in ~/.kube/config.
	KUBECONFIG="$(KUBECONFIG)" helm uninstall \
		--namespace giantswarm \
		aws-resolver-rules-operator

.PHONY: start-localstack
start-localstack: $(DOCKER_COMPOSE) ## Run localstack with docker-compose
	$(DOCKER_COMPOSE) up --detach --wait

.PHONY: stop-localstack
stop-localstack: $(DOCKER_COMPOSE) ## Run localstack with docker-compose
	$(DOCKER_COMPOSE) stop

.PHONY: test-all
test-all: test-unit test-integration ## Run all tests

.PHONY: coverage-html
coverage-html: test-unit
	go tool cover -html coverprofile.out

CONTROLLER_GEN = $(shell pwd)/bin/controller-gen
.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-get-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@v0.10.0)

ENVTEST = $(shell pwd)/bin/setup-envtest
.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t ${IMG} .


clean-tools:
	rm -rf bin

clean: clean-tools

GINKGO = $(shell pwd)/bin/ginkgo
.PHONY: ginkgo
ginkgo: ## Download ginkgo locally if necessary.
	$(call go-get-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo@latest)

$(DOCKER_COMPOSE): ## Download docker-compose locally if necessary.
	$(eval LATEST_RELEASE = $(shell curl -s https://api.github.com/repos/docker/compose/releases/latest | jq -r '.tag_name'))
	curl -fsSL "https://github.com/docker/compose/releases/download/$(LATEST_RELEASE)/docker-compose-$(shell go env GOOS)-$(shell go env GOARCH | sed 's/amd64/x86_64/; s/arm64/aarch64/')" -o $(DOCKER_COMPOSE)
	chmod +x $(DOCKER_COMPOSE)

CLUSTERCTL = $(shell pwd)/bin/clusterctl
.PHONY: clusterctl
clusterctl: ## Download clusterctl locally if necessary.
	$(call go-get-tool,$(CLUSTERCTL),sigs.k8s.io/cluster-api/cmd/clusterctl@latest)

KIND = $(shell pwd)/bin/kind
.PHONY: kind
kind: ## Download kind locally if necessary.
	$(call go-get-tool,$(KIND),sigs.k8s.io/kind@latest)

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef
