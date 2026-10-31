FROM golang:1.24 AS builder
WORKDIR /build
COPY . .
RUN CGO_ENABLED=0 go build -o /hcp-infra .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /hcp-infra /hcp-infra
ENTRYPOINT ["/hcp-infra"]
