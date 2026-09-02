# Reference server image: built from this repository by CI and by
# scripts/testdb.sh ddm-up. Never pulled from a third party (decision record 0025).
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mdmserver ./cmd/mdmserver
# The runtime image has no shell, so the data directory is prepared here and
# copied in with the runtime user's ownership.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mdmserver /mdmserver
COPY --from=build --chown=nonroot:nonroot /out/data /data
VOLUME ["/data"]
EXPOSE 8080
ENV MDM_LISTEN=:8080 MDM_STORAGE=sqlite MDM_DSN=/data/mdm.db
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/mdmserver", "-check", "http://127.0.0.1:8080/healthz"]
USER nonroot:nonroot
ENTRYPOINT ["/mdmserver"]
