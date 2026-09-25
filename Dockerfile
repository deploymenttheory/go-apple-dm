# Reference server image: built from this repository by CI and by
# scripts/testdb.sh ddm-up. See decision record 0025 for the role split and 0060
# for the published images.
#
# The build stage runs on the builder's own architecture and cross-compiles to the
# requested one. Every dependency is pure Go, including the SQLite driver, so
# CGO_ENABLED=0 cross-compiles without a toolchain per architecture and a
# multi-architecture build costs one compile per target instead of an emulated
# build per target. TARGETOS and TARGETARCH are empty under the classic builder,
# which leaves the Go defaults and produces a native binary.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS
ARG TARGETARCH
# The release version reported by `dmserver --version` and `dmctl version`. Left empty
# both binaries report "(devel)", which is correct for a development build and wrong for
# a published release, so the publish workflow passes the release version here. Keep the
# symbol in step with .goreleaser.yaml, which stamps the same one for the binaries.
ARG VERSION=""
WORKDIR /src
# The reference server is its own module and depends on the library module in
# this same repository, so both go.mod files and the workspace come first.
COPY go.mod go.sum go.work ./
COPY server/go.mod server/go.sum ./server/
RUN go mod download all
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X github.com/deploymenttheory/go-apple-dm/server/internal/buildinfo.releaseVersion=${VERSION}" \
    -o /out/dmserver ./server/cmd/dmserver
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X github.com/deploymenttheory/go-apple-dm/server/internal/buildinfo.releaseVersion=${VERSION}" \
    -o /out/dmctl ./server/cmd/dmctl
# The runtime image has no shell, so the data directory is prepared here and
# copied in with the runtime user's ownership.
RUN mkdir -p /out/data

# Only the one-shot quick-start helper needs Python; the server stays distroless.
FROM python:3.13-slim AS quickstart
COPY --from=build /out/dmctl /usr/local/bin/dmctl
COPY deploy/quickstart/bootstrap.py /bootstrap.py
RUN mkdir /data && chown 65532:65532 /data
USER 65532:65532
ENTRYPOINT ["python3", "/bootstrap.py"]

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/dmserver /dmserver
COPY --from=build /out/dmctl /dmctl
COPY --from=build --chown=nonroot:nonroot /out/data /data
VOLUME ["/data"]
EXPOSE 8080
# Remote container listeners require DM_LISTEN plus a configured TLS certificate/key.
ENV DM_LISTEN=127.0.0.1:8080 DM_STORAGE=sqlite DM_DSN=/data/dm.db
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/dmserver", "-check", "auto"]
USER nonroot:nonroot
ENTRYPOINT ["/dmserver"]
