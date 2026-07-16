.PHONY: help build backend frontend frontend-bundle dev dev-frontend test test-go test-frontend e2e e2e-build e2e-web e2e-cli lint lint-json logpolicy pathpolicy literalpolicy architecturepolicy literalpolicy-fix precommit prepush fmt fmt-go fmt-frontend gofix gofix-check gofix-changed gofix-check-changed commitmsg-check clean

ifeq ($(OS),Windows_NT)
EXE := .exe
FULL_BUILD := powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/build.ps1
MKDIR_DIST := powershell -NoProfile -Command "New-Item -ItemType Directory -Force -Path dist | Out-Null"
RM_DIST := powershell -NoProfile -Command "Remove-Item -Recurse -Force dist, webui/dist -ErrorAction SilentlyContinue"
BLANK := echo.
else
EXE :=
FULL_BUILD := ./scripts/build.sh
MKDIR_DIST := mkdir -p dist
RM_DIST := rm -rf dist webui/dist
BLANK := echo
endif

CLI_OUT := dist/upbrr$(EXE)
E2E_CLI_OUT := dist/upbrr-e2e$(EXE)
GO_TEST_FLAGS := -race -v -timeout 20m
GOLANGCI_FLAGS := --timeout=5m
GO_CHANGED_FILES := $(shell git diff --name-only --diff-filter=ACMR HEAD -- '*.go')
GO_CHANGED_PKGS := $(addprefix ./,$(sort $(patsubst %/,%,$(dir $(GO_CHANGED_FILES)))))

help:
	@echo Build
	@echo   make build              Full build: WebUI, embedded assets, CLI
	@echo   make backend            Build CLI binary only
	@echo   make frontend           Typecheck and build frontend bundle
	@echo   make frontend-bundle    Build frontend bundle only
	@$(BLANK)
	@echo Development
	@echo   make dev                Start WebUI server without auth on loopback
	@echo   make dev-frontend       Start Vite dev server only
	@$(BLANK)
	@echo Testing
	@echo   make test               Run Go and frontend checks
	@echo   make test-go            Run full Go test suite with race detector
	@echo   make test-frontend      Run frontend lint/type/format/dead-code/unit checks
	@echo   make e2e                Run all Playwright E2E projects
	@echo   make e2e-web            Run embedded web E2E projects
	@echo   make e2e-cli            Run CLI full-upload E2E project
	@$(BLANK)
	@echo Linting
	@echo   make lint               Run architecture/path/literal policies and full Go lint
	@echo   make architecturepolicy Run architecture ownership policy check
	@echo   make lint-json          Write Go lint JSON to lint-report.json
	@echo   make logpolicy          Run logging policy check
	@echo   make pathpolicy         Run path portability policy check
	@$(BLANK)
	@echo Pre-commit
	@echo   make precommit          Run Lefthook pre-commit
	@echo   make prepush            Run Lefthook pre-push
	@$(BLANK)
	@echo Formatting
	@echo   make fmt                Run Go formatter and frontend Prettier
	@echo   make fmt-go             Run configured Go formatters
	@echo   make fmt-frontend       Run frontend Prettier
	@echo   make gofix              Apply reviewed Go fixes with omitzero disabled
	@echo   make gofix-check        Check Go fix drift with omitzero disabled
	@echo   make gofix-changed      Apply Go fixes to changed packages
	@echo   make gofix-check-changed Check Go fix drift on changed packages

build:
	$(FULL_BUILD)

backend:
	$(MKDIR_DIST)
	go build -o $(CLI_OUT) ./cmd/upbrr

frontend:
	pnpm --dir webui run build

frontend-bundle:
	pnpm --dir webui run build:bundle

dev:
	go run ./cmd/upbrr serve --dev-no-auth

dev-frontend:
	pnpm --dir webui run dev

test: test-go test-frontend

test-go:
	go test $(GO_TEST_FLAGS) ./...

test-frontend:
	pnpm --dir webui run lint
	pnpm --dir webui run lint:dead
	pnpm --dir webui run typecheck
	pnpm --dir webui run test:unit
	pnpm --dir webui run format:check

e2e: e2e-build
	pnpm --dir webui run test:e2e:full

e2e-build:
	pnpm --dir webui install --frozen-lockfile
	pnpm --dir webui run build
ifeq ($(OS),Windows_NT)
	pwsh -NoProfile -File ./scripts/sync-webui-assets.ps1
else
	sh ./scripts/sync-webui-assets.sh
endif
	$(MKDIR_DIST)
	go build -tags e2e -o $(E2E_CLI_OUT) ./cmd/upbrr

e2e-web: e2e-build
	pnpm --dir webui run test:e2e:web

e2e-cli: e2e-build
	pnpm --dir webui exec playwright test --project=cli-full-upload

lint: architecturepolicy pathpolicy literalpolicy
	golangci-lint run $(GOLANGCI_FLAGS) ./...

lint-json:
	golangci-lint run $(GOLANGCI_FLAGS) --output.json.path lint-report.json ./...

logpolicy:
	go run ./cmd/logpolicy

pathpolicy:
	go run ./cmd/pathpolicy

literalpolicy:
	go run ./cmd/literalpolicy

architecturepolicy:
	go run ./cmd/architecturepolicy

literalpolicy-fix:
	go run ./cmd/literalpolicy -fix
	golangci-lint fmt

precommit:
	lefthook run pre-commit
	git diff --check
	$(MAKE) gofix-check-changed
	$(MAKE) lint
	$(MAKE) logpolicy
	$(MAKE) test-frontend

prepush:
	lefthook run pre-push

fmt: fmt-go fmt-frontend

fmt-go:
	go run ./cmd/literalpolicy -fix
	golangci-lint fmt

fmt-frontend:
	pnpm --dir webui run format

gofix:
	go fix -omitzero=false ./...

gofix-check:
	go fix -diff -omitzero=false ./...

gofix-changed:
ifeq ($(strip $(GO_CHANGED_PKGS)),)
	@echo No changed Go files
else
	go fix -omitzero=false $(GO_CHANGED_PKGS)
endif

gofix-check-changed:
ifeq ($(strip $(GO_CHANGED_PKGS)),)
	@echo No changed Go files
else
	go fix -diff -omitzero=false $(GO_CHANGED_PKGS)
endif

commitmsg-check:
	go run ./cmd/commitmsgcheck $(MSG)

clean:
	$(RM_DIST)
