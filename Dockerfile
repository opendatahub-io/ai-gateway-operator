# Build the manager binary
FROM registry.access.redhat.com/ubi10/go-toolset:latest AS builder
ARG TARGETOS
ARG TARGETARCH
ARG LDFLAGS=""

USER 0
WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# Cache deps before building and copying source so that we don't need to
# re-download as much and so that source changes don't invalidate our
# downloaded layer.
RUN go mod download

# Copy only the source and build inputs needed for the binary and manifests.
COPY Makefile Makefile
COPY api/ api/
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/
COPY config/manifests/ config/manifests/

# Generated code and manifests come from the host (make container-prep).
# Only compile the manager binary inside the image.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-$(go env GOARCH)} \
    make build-bin BIN_DIR=/workspace/bin BIN_NAME=manager LDFLAGS="${LDFLAGS}"

# Make manifests readable by any user (OpenShift assigns arbitrary UIDs)
RUN chmod -R a+rX config/manifests/

# Use UBI 10 micro as minimal runtime image
FROM registry.access.redhat.com/ubi10/ubi-micro:latest
WORKDIR /
COPY --from=builder /workspace/bin/manager .
COPY --from=builder /workspace/config/manifests/ /manifests/
USER 65532:65532

ENTRYPOINT ["/manager"]
