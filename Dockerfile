FROM golang:1.22-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /immich-dlna-proxy .

FROM alpine:3.20
COPY --from=build /immich-dlna-proxy /usr/local/bin/immich-dlna-proxy
# HTTP part (description.xml, SOAP control, media streaming)
EXPOSE 8200/tcp
# SSDP discovery
EXPOSE 1900/udp
VOLUME ["/config/cache"]
ENTRYPOINT ["/usr/local/bin/immich-dlna-proxy"]
