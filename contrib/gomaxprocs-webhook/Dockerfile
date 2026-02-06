FROM registry.ci.openshift.org/openshift/release:rhel-9-release-golang-1.24-openshift-4.22 AS builder
WORKDIR /workspace

# Force module mode and leverage Go module caching
ENV GOFLAGS=-mod=mod
COPY go.mod go.sum ./
RUN go mod download

# Copy only the gomaxprocs-webhook sources
COPY . ./

RUN make build

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.7-1762180032
COPY --from=builder /workspace/bin/gomaxprocs-webhook /usr/bin/gomaxprocs-webhook
USER 1001

ENTRYPOINT ["/usr/bin/gomaxprocs-webhook", "serve"]