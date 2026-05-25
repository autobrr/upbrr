.PHONY: help build backend frontend frontend-bundle gui dev dev-frontend test test-go test-frontend lint lint-json logpolicy precommit prepush fmt fmt-go fmt-frontend gofix gofix-check gofix-changed gofix-check-changed commitmsg-check clean

ifeq ($(OS),Windows_NT)
EXE := .exe
FULL_BUILD := powershell -NoProfile -ExecutionPolicy Bypass -File ./scripts/build.ps1
MKDIR_DIST := powershell -NoProfile -Command "New-Item -ItemType Directory -Force -Path dist | Out-Null"
RM_DIST := powershell -NoProfile -Command "Remove-Item -Recurse -Force dist, gui/frontend/dist, gui/build/bin -ErrorAction SilentlyContinue"
WAILS_PLATFORM := windows/amd64
BLANK := echo.
else
EXE :=
FULL_BUILD := ./scripts/build.sh
MKDIR_DIST := mkdir -p dist
RM_DIST := rm -rf dist gui/frontend/dist gui/build/bin
WAILS_PLATFORM :=
BLANK := echo
endif

CLI_OUT := dist/upbrr$(EXE)
GO_TEST_FLAGS := -v -timeout 20m
GOLANGCI_FLAGS := --timeout=5m
GO_CHANGED_FILES := $(shell git diff --name-only --diff-filter=ACMR HEAD -- '*.go')
GO_CHANGED_PKGS := $(sort $(patsubst %/,%,$(dir $(GO_CHANGED_FILES))))

help:
	@echo Build
	@echo   make build              Full build: frontend, embedded assets, CLI, Wails GUI
	@echo   make backend            Build CLI binary only
	@echo   make frontend           Typecheck and build frontend bundle
	@echo   make frontend-bundle    Build frontend bundle only
	@echo   make gui                Build Wails GUI with current embedded assets
	@$(BLANK)
	@echo Development
	@echo   make dev                Start Wails dev mode
	@echo   make dev-frontend       Start Vite dev server only
	@$(BLANK)
	@echo Testing
	@echo   make test               Run Go and frontend checks
	@echo   make test-go            Run full Go test suite
	@echo   make test-frontend      Run frontend lint/type/format/dead-code checks
	@$(BLANK)
	@echo Linting
	@echo   make lint               Run full Go lint
	@echo   make lint-json          Write Go lint JSON to lint-report.json
	@echo   make logpolicy          Run logging policy check
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
	pnpm --dir gui/frontend run build

frontend-bundle:
	pnpm --dir gui/frontend run build:bundle

gui:
	go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.1
ifeq ($(WAILS_PLATFORM),)
	cd gui && wails build
else
	cd gui && wails build -platform $(WAILS_PLATFORM)
endif

dev:
	cd gui && wails dev

dev-frontend:
	pnpm --dir gui/frontend run dev

test: test-go test-frontend

test-go:
	go test $(GO_TEST_FLAGS) ./...

test-frontend:
	pnpm --dir gui/frontend run lint
	pnpm --dir gui/frontend run lint:dead
	pnpm --dir gui/frontend run typecheck
	pnpm --dir gui/frontend run format:check

lint:
	golangci-lint run $(GOLANGCI_FLAGS) ./...

lint-json:
	golangci-lint run $(GOLANGCI_FLAGS) --output.json.path lint-report.json ./...

logpolicy:
	go run ./cmd/logpolicy

precommit:
	lefthook run pre-commit

prepush:
	lefthook run pre-push

fmt: fmt-go fmt-frontend

fmt-go:
	golangci-lint fmt

fmt-frontend:
	pnpm --dir gui/frontend run format

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
