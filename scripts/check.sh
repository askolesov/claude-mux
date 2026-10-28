#!/usr/bin/env bash
# Every check CI runs (.github/workflows/ci.yml), here:
#   scripts/check.sh
# Needs Go, golangci-lint 2.x and Node (npm install once, for ESLint). On a
# Mac it also builds the hub with its window.
set -euo pipefail
cd "$(dirname "$0")/.."

step() { printf '\n== %s\n' "$*"; }

step gofmt
test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
step "go vet (linux, windows, darwin)"
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 GOOS=windows go vet ./...
CGO_ENABLED=0 GOOS=darwin go vet ./...
step golangci-lint
CGO_ENABLED=0 golangci-lint run ./...
CGO_ENABLED=0 GOOS=windows golangci-lint run ./...
step eslint
[ -d node_modules ] || npm ci --silent
npx eslint
step "go test, and -race on the internals"
CGO_ENABLED=0 go test ./...
CGO_ENABLED=1 go test -race ./internal/...
if [ "$(uname)" = Darwin ]; then
	step "the hub with its window (cgo)"
	CGO_ENABLED=1 CGO_CXXFLAGS=-Wno-deprecated-literal-operator golangci-lint run ./...
	CGO_ENABLED=1 CGO_CXXFLAGS=-Wno-deprecated-literal-operator go build -o /dev/null ./cmd/hub
	CGO_ENABLED=1 CGO_CXXFLAGS=-Wno-deprecated-literal-operator go test -race ./cmd/...
fi
printf '\nall checks passed\n'
