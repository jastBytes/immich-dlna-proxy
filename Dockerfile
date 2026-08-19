FROM golang:1.26.5-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /immich-dlna-proxy . && \
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
ENTRYPOINT ["/usr/local/bin/immich-dlna-proxy"]
