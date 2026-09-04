# Reference server image: built from this repository by CI and by
# scripts/testdb.sh ddm-up. Never pulled from a third party (decision record 0025).
FROM golang:1.27 AS build
WORKDIR /src
# The reference server is its own module and depends on the library module in
# this same repository, so both go.mod files and the workspace come first.
COPY go.mod go.sum go.work ./
COPY server/go.mod server/go.sum ./server/
RUN go mod download all
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dmserver ./server/cmd/dmserver
# The runtime image has no shell, so the data directory is prepared here and
# copied in with the runtime user's ownership.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/dmserver /dmserver
COPY --from=build --chown=nonroot:nonroot /out/data /data
VOLUME ["/data"]
EXPOSE 8080
ENV DM_LISTEN=:8080 DM_STORAGE=sqlite DM_DSN=/data/dm.db
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/dmserver", "-check", "http://127.0.0.1:8080/healthz"]
USER nonroot:nonroot
ENTRYPOINT ["/dmserver"]
