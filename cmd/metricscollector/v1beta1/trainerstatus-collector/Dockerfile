# Build the collector binary
FROM golang:1.22 AS builder

WORKDIR /workspace

# Copy go mod files
COPY go.mod go.mod
COPY go.sum go.sum

# Download dependencies
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o trainerstatus-collector ./cmd/metricscollector/v1beta1/trainerstatus-collector

# Use distroless as minimal base image
FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /workspace/trainerstatus-collector .

USER 65532:65532

ENTRYPOINT ["/trainerstatus-collector"]
