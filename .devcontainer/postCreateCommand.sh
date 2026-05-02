#!/usr/bin/env bash
set -euo pipefail

# Pre-warm the Go module cache so the first `go test` is instant.
go mod download

# Fix ownership of the Claude Code volume — Docker creates volumes as root
# but devcontainer runs as the vscode user.
sudo chown -R "$(id -u)":"$(id -g)" /home/vscode/.claude || true

# Install Claude Code CLI (idempotent — overwrites if already present).
curl -fsSL https://claude.ai/install.sh | bash || true

echo "devcontainer ready: go $(go version), golangci-lint $(golangci-lint --version | head -n1)"
