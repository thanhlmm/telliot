include .bingo/Variables.mk

FILES_TO_FMT      ?= $(shell find . -name '*.go' -print)


# Ensure everything works even if GOPATH is not set, which is often the case.
# The `go env GOPATH` will work for all cases for Go 1.8+.
GOPATH            ?= $(shell go env GOPATH)
GOBIN             ?= $(firstword $(subst :, ,${GOPATH}))/bin

# Support gsed on OSX (installed via brew), falling back to sed. On Linux
# systems gsed won't be installed, so will use sed as expected.
SED     ?= $(shell which gsed 2>/dev/null || which sed)
GIT     ?= $(shell which git)
GIT_TAG  = $(shell git describe --tags)
GIT_HASH = $(shell git rev-parse --short HEAD)

export GO111MODULE=on
GOPROXY           ?= https://proxy.golang.org
export GOPROXY

GOTEST_OPTS ?= -failfast -timeout 10m -v
TMP_DIR ?= /tmp
OS ?= $(shell uname -s | tr '[A-Z]' '[a-z]')
ARCH ?= $(shell uname -m)

define require_clean_work_tree
	@git update-index -q --ignore-submodules --refresh

	@if ! git diff-files --quiet --ignore-submodules --; then \
		echo >&2 "cannot $1: you have unstaged changes."; \
		git diff-files --name-status -r --ignore-submodules -- >&2; \
		echo >&2 "Please commit or stash them."; \
		exit 1; \
	fi

	@if ! git diff-index --cached --quiet HEAD --ignore-submodules --; then \
		echo >&2 "cannot $1: your index contains uncommitted changes."; \
		git diff-index --cached --name-status -r --ignore-submodules HEAD -- >&2; \
		echo >&2 "Please commit or stash them."; \
		exit 1; \
	fi

endef

help: ## Displays help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-z0-9A-Z_-]+:.*?##/ { printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: deps
deps: ## Ensures fresh go.mod and go.sum.
	@go mod tidy
	@go mod verify

.PHONY: generate
generate: ## Ensures kernelSource.go is generated.
	@go run ./scripts/opencl

.PHONY: build
build: check-git generate
ifeq ($(GIT_TAG),)
	@echo "GIT_TAG is empty" && exit 1
endif
ifeq ($(GIT_HASH),)
	@echo "GIT_HASH is empty" && exit 1
endif
	@go build -v -ldflags "-X main.GitTag=${GIT_TAG} -X main.GitHash=${GIT_HASH} -s -w" ./cmd/tellor

.PHONY: check-git
check-git:
ifneq ($(GIT),)
	@test -x $(GIT) || (echo >&2 "No git executable binary found at $(GIT)."; exit 1)
else
	@echo >&2 "No git binary found."; exit 1
endif

.PHONY: test
test: generate
	go test $(GOTEST_OPTS) ./...

.PHONY: go-format
go-format: ## Formats Go code including imports.
go-format: $(GOIMPORTS)
	@echo ">> formatting go code"
	@SED_BIN="$(SED)" scripts/cleanup-white-noise.sh $(FILES_TO_FMT)
	@gofmt -s -w $(FILES_TO_FMT)
	@$(GOIMPORTS) -w $(FILES_TO_FMT)

.PHONY:format
format: ## Formats code including imports and cleans up white noise.
format: go-format
	@SED_BIN="$(SED)" scripts/cleanup-white-noise.sh $(FILES_TO_FMT)

.PHONY:lint
lint: ## Runs various static analysis against our code.
lint: go-lint shell-lint
	@echo ">> detecting white noise"
	@find . -type f \( -name "*.md" -o -name "*.go" \) | SED_BIN="$(SED)" xargs scripts/cleanup-white-noise.sh
	$(call require_clean_work_tree,'detected white noise, run make lint and commit changes')

# PROTIP:
# Add
#      --cpu-profile-path string   Path to CPU profile output file
#      --mem-profile-path string   Path to memory profile output file
# to debug big allocations during linting.
.PHONY: go-lint
go-lint: generate check-git deps $(GOLANGCI_LINT) $(FAILLINT)
	$(call require_clean_work_tree,'detected not clean master before running lint, previous job changed something?')
	@echo ">> verifying modules being imported"
	@echo ">> linting all of the Go files GOGC=${GOGC}"
	@$(GOLANGCI_LINT) run
	@echo ">> ensuring Copyright headers"
	@go run ./scripts/copyright
	$(call require_clean_work_tree,'detected files without copyright, run make lint and commit changes')

SHELLCHECK ?= $(TMP_DIR)/shellcheck
$(SHELLCHECK):
	@echo "Downloading Shellcheck"
	curl -sNL "https://github.com/koalaman/shellcheck/releases/download/stable/shellcheck-stable.$(OS).$(ARCH).tar.xz" | tar --strip-components=1 -xJf - -C /tmp


.PHONY:shell-lint
shell-lint: ## Runs static analysis against our shell scripts.
shell-lint: $(SHELLCHECK)
	@echo ">> linting all of the shell script files"
	@$(SHELLCHECK) --severity=error -o all -s bash $(shell find . -type f -name "*.sh" -not -path "*vendor*" -not -path "tmp/*" -not -path "*node_modules*")

.PHONY: update-go-deps
update-go-deps:
	@echo ">> updating Go dependencies"
	@for m in $$($(GO) list -mod=readonly -m -f '{{ if and (not .Indirect) (not .Main)}}{{.Path}}{{end}}' all); do \
		$(GO) get $$m; \
	done
	$(GO) mod tidy