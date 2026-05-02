# Devcontainer image for dcallocate.
# Runtime is the bare native binary; this Dockerfile is for development only.
FROM mcr.microsoft.com/devcontainers/go:1-1.23-bookworm

# Install golangci-lint at a pinned version. Going through the upstream
# install script so the binary lands on $GOPATH/bin (already on PATH for the
# vscode user). Pinning avoids "latest" drift across rebuilds.
ARG GOLANGCI_LINT_VERSION=v1.61.0
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
    | sh -s -- -b /usr/local/bin "${GOLANGCI_LINT_VERSION}"

# Useful CLI extras when poking at PortfolioPerformance XML by hand.
# The Microsoft Go devcontainer base ships a Yarn apt source whose signing
# key has rotated; remove it so apt-get update doesn't fail on
# "NO_PUBKEY 62D54FD4003F6525" / "repository ... is not signed".
RUN rm -f /etc/apt/sources.list.d/yarn.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        less \
        jq \
        xmlstarlet \
    && rm -rf /var/lib/apt/lists/*
