FROM ghcr.io/actions/actions-runner:latest

USER root

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      make \
      gcc \
      g++ \
      curl \
      git \
    && rm -rf /var/lib/apt/lists/*

# Install Go
ARG GO_VERSION=1.25.3
RUN curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

USER runner
