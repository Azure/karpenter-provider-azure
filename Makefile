include Makefile-az.mk

export K8S_VERSION ?= 1.27.x
export KUBEBUILDER_ASSETS ?= ${HOME}/.kubebuilder/bin

# # CR for local builds of Karpenter
SYSTEM_NAMESPACE ?= karpenter

# Common Directories
# TODO: revisit testing tools (temporarily excluded here, for make verify)
MOD_DIRS = $(shell find . -path "./website" -prune -o -name go.mod -type f -print | xargs dirname)
KARPENTER_CORE_DIR = $(shell go list -m -f '{{ .Dir }}' github.com/aws/karpenter-core)

# TEST_SUITE enables you to select a specific test suite directory to run "make e2etests" or "make test" against
TEST_SUITE ?= "..."
TEST_TIMEOUT ?= "3h"

help: ## Display help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

presubmit: verify test ## Run all steps in the developer loop

ci-test: battletest coverage ## Runs tests and submits coverage

ci-non-test: verify vulncheck ## Runs checks other than tests

test: ## Run tests
	ginkgo -v --focus="${FOCUS}" ./pkg/$(shell echo $(TEST_SUITE) | tr A-Z a-z)

battletest: ## Run randomized, racing, code-covered tests
	ginkgo -v \
		-race \
		-cover -coverprofile=coverage.out -output-dir=. -coverpkg=./pkg/... \
		--focus="${FOCUS}" \
		--randomize-all \
		-tags random_test_delay \
		./pkg/...

e2etests: ## Run the e2e suite against your local cluster
	# Notes:
	# -p: the number of programs, such as build commands or test binaries, that can be run in parallel?
	# -count 1: prevents caching
	# -timeout: If a test binary runs longer than TEST_TIMEOUT, panic
	# -v: verbose output
	cd test && CLUSTER_NAME=${CLUSTER_NAME} go test \
		-p 1 \
		-count 1 \
		-timeout ${TEST_TIMEOUT} \
		-v \
		./suites/$(shell echo $(TEST_SUITE) | tr A-Z a-z)/... \
		--ginkgo.focus="${FOCUS}" \
		--ginkgo.timeout=${TEST_TIMEOUT} \
		--ginkgo.grace-period=3m \
		--ginkgo.vv

benchmark:
	go test -tags=test_performance -run=NoTests -bench=. ./...

deflake: ## Run randomized, racing, code-covered tests to deflake failures
	for i in $(shell seq 1 5); do make battletest || exit 1; done

deflake-until-it-fails: ## Run randomized, racing tests until the test fails to catch flakes
	ginkgo \
		--race \
		--focus="${FOCUS}" \
		--randomize-all \
		--until-it-fails \
		-v \
		./pkg/...

coverage:
	go tool cover -html coverage.out -o coverage.html

verify: toolchain tidy download ## Verify code. Includes dependencies, linting, formatting, etc
	go generate ./...
	hack/boilerplate.sh
	cp $(KARPENTER_CORE_DIR)/pkg/apis/crds/* pkg/apis/crds
	yq -i '(.spec.versions[0].additionalPrinterColumns[] | select (.name=="Zone")) .jsonPath=".metadata.labels.karpenter\.azure\.com/zone"' \
		pkg/apis/crds/karpenter.sh_nodeclaims.yaml
	$(foreach dir,$(MOD_DIRS),cd $(dir) && golangci-lint run $(newline))
	@git diff --quiet ||\
		{ echo "New file modification detected in the Git working tree. Please check in before commit."; git --no-pager diff --name-only | uniq | awk '{print "  - " $$0}'; \
		if [ "${CI}" = true ]; then\
			exit 1;\
		fi;}
	# TODO: restore codegen if needed; decide on the future of docgen
	#@echo "Validating codegen/docgen build scripts..."
	#@find hack/code hack/docs -name "*.go" -type f -print0 | xargs -0 -I {} go build -o /dev/null {}

vulncheck: ## Verify code vulnerabilities
	@govulncheck ./pkg/...

codegen: ## Auto generate files based on Azure API responses
	./hack/codegen.sh

toolchain: ## Install developer toolchain
	./hack/toolchain.sh

website: ## Serve the docs website locally
	cd website && npm install && hugo mod tidy && hugo server

tidy: ## Recursively "go mod tidy" on all directories where go.mod exists
	$(foreach dir,$(MOD_DIRS),cd $(dir) && go mod tidy $(newline))

download: ## Recursively "go mod download" on all directories where go.mod exists
	$(foreach dir,$(MOD_DIRS),cd $(dir) && go mod download $(newline))

.PHONY: help test battletest e2etests verify tidy download codegen toolchain vulncheck

define newline


endef
