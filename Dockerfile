FROM golang:1.26.5-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY . .
# Version reported to DLNA clients and in the startup log; release.yml passes
# the tag (e.g. v0.2.0), CI builds pass the commit.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /immich-dlna-proxy . && \
    mkdir -p /config/cache && chown -R 65532:65532 /config

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /config /config
COPY --from=build /immich-dlna-proxy /usr/local/bin/immich-dlna-proxy
# HTTP part (description.xml, SOAP control, media streaming)
EXPOSE 8200/tcp
# SSDP discovery
EXPOSE 1900/udp
VOLUME ["/config/cache"]
USER 65532:65532
# The binary probes its own /healthz (no curl in a scratch image): healthy
# when the proxy is up and Immich answers.
HEALTHCHECK --interval=30s --timeout=15s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/immich-dlna-proxy", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/immich-dlna-proxy"]
