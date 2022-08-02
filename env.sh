#! /usr/bin/env bash

set -euo pipefail

EMBEDMD_VERSION='v2.0.0'
go install "github.com/campoy/embedmd/v2@${EMBEDMD_VERSION}"

GOFUMPT_VERSION='v0.3.1'
go install "mvdan.cc/gofumpt@${GOFUMPT_VERSION}"

GOLANGCI_LINT_VERSION='v1.47.2'
go install "github.com/golangci/golangci-lint/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"
