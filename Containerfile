# Build the manager binary
FROM registry.access.redhat.com/ubi10/go-toolset:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG LDFLAGS=""
ARG GIT_COMMIT=""
ARG GIT_BRANCH=""
ARG GIT_REPO=""
ARG VERSION=""

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
# only the sub-modules need to be copied here
COPY config/manifests/batchgateway/ config/manifests/batchgateway/
COPY config/manifests/maascontroller/ config/manifests/maascontroller/

# Generated code and manifests come from the host (make container-prep).
# Only compile the manager binary inside the image.
RUN VERSION_PKG="github.com/opendatahub-io/ai-gateway-operator/pkg/version" && \
    if [ -z "${LDFLAGS}" ] && [ -n "${GIT_COMMIT}" ]; then \
      LDFLAGS="-X ${VERSION_PKG}.Version=${VERSION} -X ${VERSION_PKG}.Commit=${GIT_COMMIT} -X ${VERSION_PKG}.Branch=${GIT_BRANCH} -X ${VERSION_PKG}.Repo=${GIT_REPO}"; \
    fi && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-$(go env GOARCH)} \
    make build-bin BIN_DIR=/workspace/bin BIN_NAME=manager LDFLAGS="${LDFLAGS}"

# Make manifests readable by any user (OpenShift assigns arbitrary UIDs)
RUN chmod -R a+rX config/manifests/

# Use UBI 10 micro as minimal runtime image
FROM registry.access.redhat.com/ubi10/ubi-micro:10.0
WORKDIR /
COPY --from=builder /workspace/bin/manager .
COPY --from=builder /workspace/config/manifests/ /manifests/
USER 65532:65532

ENTRYPOINT ["/manager"]
