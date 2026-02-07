include Makefile-az.mk

## Inject the app version into operator.Version
LDFLAGS ?= -ldflags=-X=sigs.k8s.io/karpenter/pkg/operator.Version=$(shell git describe --tags --always | cut -d"v" -f2)

GOFLAGS ?= $(LDFLAGS)

# # CR for local builds of Karpenter
KARPENTER_NAMESPACE ?= kube-system

# Common Directories
# TODO: revisit testing tools (temporarily excluded here, for make verify)
MOD_DIRS = $(shell find . -name go.mod -type f ! -path "./test/*" | xargs dirname)
KARPENTER_CORE_DIR = $(shell go list -m -f '{{ .Dir }}' sigs.k8s.io/karpenter)

# TEST_SUITE enables you to select a specific test suite directory to run "make e2etests" or "make test" against
TEST_SUITE ?= "..."
TEST_TIMEOUT ?= "3h"

help: ## Display help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

presubmit: verify test ## Run all steps in the developer loop

ci-test: test coverage ## Runs tests and submits coverage

ci-non-test: verify licenses vulncheck ## Runs checks other than tests

test: ## Run tests
	ginkgo -vv \
		-cover -coverprofile=coverage.out -output-dir=. -coverpkg=./pkg/... \
		--focus="${FOCUS}" \
		--randomize-all \
		./pkg/...

deflake: ## Run randomized, racing, code-covered tests to deflake failures
	for i in $(shell seq 1 5); do make test || exit 1; done

deflake-until-it-fails: ## Run randomized, racing tests until the test fails to catch flakes
	ginkgo \
		--race \
		--focus="${FOCUS}" \
		--randomize-all \
		--until-it-fails \
		-v \
		./pkg/...

e2etests: ## Run the e2e suite against your local cluster
	# Notes:
	# -p: the number of programs, such as build commands or test binaries, that can be run in parallel?
	# -count 1: prevents caching
	# -timeout: If a test binary runs longer than TEST_TIMEOUT, panic
	# -v: verbose output
	cd test && AZURE_CLUSTER_NAME=${AZURE_CLUSTER_NAME} AZURE_ACR_NAME=${AZURE_ACR_NAME} AZURE_RESOURCE_GROUP=${AZURE_RESOURCE_GROUP} AZURE_SUBSCRIPTION_ID=${AZURE_SUBSCRIPTION_ID} AZURE_LOCATION=${AZURE_LOCATION} VNET_RESOURCE_GROUP=${VNET_RESOURCE_GROUP} go test \
		-p 1 \
		-count 1 \
		-timeout ${TEST_TIMEOUT} \
		-v \
		./suites/$(shell echo $(TEST_SUITE) | tr A-Z a-z)/... \
		--ginkgo.focus="${FOCUS}" \
		--ginkgo.timeout=${TEST_TIMEOUT} \
		--ginkgo.grace-period=3m \
		--ginkgo.vv

upstream-e2etests: tidy download
	AZURE_CLUSTER_NAME=${AZURE_CLUSTER_NAME} AZURE_ACR_NAME=${AZURE_ACR_NAME} AZURE_RESOURCE_GROUP=${AZURE_RESOURCE_GROUP} AZURE_SUBSCRIPTION_ID=${AZURE_SUBSCRIPTION_ID} AZURE_LOCATION=${AZURE_LOCATION} VNET_RESOURCE_GROUP=${VNET_RESOURCE_GROUP} \
	cd $(KARPENTER_CORE_DIR) && go test \
		-count 1 \
		-timeout 1h \
		-v \
		./test/suites/... \
		--ginkgo.focus="${FOCUS}" \
		--ginkgo.timeout=1h \
		--ginkgo.grace-period=5m \
		--ginkgo.vv \
		--default-nodeclass="$(shell pwd)/test/pkg/environment/azure/default_aksnodeclass.yaml" \
		--default-nodepool="$(shell pwd)/test/pkg/environment/azure/default_nodepool.yaml"

benchmark:
	go test -tags=test_performance -run=NoTests -bench=. ./...

coverage:
	go tool cover -html coverage.out -o coverage.html

verify: tidy download ## Verify code. Includes dependencies, linting, formatting, etc
	SKIP_INSTALLED=true make toolchain
	make az-swagger-generate-clients-raw
	go generate ./...
	hack/boilerplate.sh
	cp $(KARPENTER_CORE_DIR)/pkg/apis/crds/* pkg/apis/crds
	hack/validation/kubelet.sh
	hack/validation/labels.sh
	hack/validation/requirements.sh
	hack/mutation/kubectl_get_ux.sh
	cp pkg/apis/crds/* charts/karpenter-crd/templates
	hack/github/dependabot.sh
	$(foreach dir,$(MOD_DIRS),cd $(dir) && golangci-lint-custom run $(newline))
	@git diff --quiet ||\
		{ echo "New file modification detected in the Git working tree. Please check in before commit."; git --no-pager diff --name-only | uniq | awk '{print "  - " $$0}'; \
		if [ "${CI}" = true ]; then\
			exit 1;\
		fi;}
	# TODO: restore codegen if needed; decide on the future of docgen
	#@echo "Validating codegen/docgen build scripts..."
	#@find hack/code hack/docs -name "*.go" -type f -print0 | xargs -0 -I {} go build -o /dev/null {}
	actionlint -oneline

vulncheck: ## Verify code vulnerabilities
	@govulncheck ./pkg/...
	@trivy filesystem --ignore-unfixed --scanners vuln --exit-code 1 go.mod

licenses: download ## Verifies dependency licenses
	! go-licenses csv ./... | grep -v -e 'MIT' -e 'Apache-2.0' -e 'BSD-3-Clause' -e 'BSD-2-Clause' -e 'ISC' -e 'MPL-2.0'

codegen: ## Auto generate files based on Azure API responses
	./hack/codegen.sh

snapshot: az-login ## Builds and publishes snapshot release
	./hack/release/snapshot.sh

release: az-login ## Builds and publishes stable release
	./hack/release/release.sh

toolchain: ## Install developer toolchain
	./hack/toolchain.sh

tidy: ## Recursively "go mod tidy" on all directories where go.mod exists
	$(foreach dir,$(MOD_DIRS),cd $(dir) && go mod tidy $(newline))

download: ## Recursively "go mod download" on all directories where go.mod exists
	$(foreach dir,$(MOD_DIRS),cd $(dir) && go mod download $(newline))

.PHONY: help presubmit ci-test ci-non-test test deflake deflake-until-it-fails e2etests upstream-e2etests coverage verify vulncheck licenses codegen snapshot release toolchain tidy download

define newline


endef
