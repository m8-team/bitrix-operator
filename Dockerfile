# Patched Go toolchain, pinned to the official multi-platform image index.
ARG BASE_IMAGE=golang:1.27.3-bookworm@sha256:dc0963f32053e4bc451d24d87e3892d42d01021b61f093e98eb95c0352bb84b0
ARG RUNTIME_IMAGE=gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
FROM --platform=$BUILDPLATFORM ${BASE_IMAGE} AS builder
ARG TARGETOS
ARG TARGETARCH
ENV GOTOOLCHAIN=local
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -buildvcs=false -ldflags="-s -w" -o manager ./cmd

FROM ${RUNTIME_IMAGE}
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
