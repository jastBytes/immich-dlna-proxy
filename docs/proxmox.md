# Running on Proxmox VE

The proxy is a single static binary, so the lightest way to run it on
Proxmox is an **unprivileged LXC container** running the release binary
as a systemd service - no Docker needed. If you'd rather use the
published Docker image, see [Alternative: Docker](#alternative-docker).

## What matters for DLNA

TVs find the proxy via SSDP, which is UDP multicast on port 1900. That
only works if the container sits **directly on the same LAN segment as
your TVs**:

- Attach the container's network interface to the bridge that is
  connected to your LAN (usually `vmbr0`), with its own address on the
  LAN (DHCP or a static IP). Don't put it behind NAT or on an internal-only
  bridge - multicast won't reach it.
- If the **Proxmox firewall** is enabled for the container (or at the
  datacenter/node level with a restrictive policy), allow inbound
  **UDP 1900** (SSDP) and **TCP 8200** (or whatever port `LISTEN_ADDR`
  uses).
- If the container's VLAN tag differs from the TVs' VLAN, discovery won't
  work without a multicast relay between the VLANs.

## 1. Create the LXC container

In the Proxmox web UI (*Create CT*), or on the Proxmox host shell:

```bash
# Download a Debian template once (pick the current version listed by `pveam available`)
pveam update
pveam available --section system | grep debian
pveam download local debian-12-standard_12.7-1_amd64.tar.zst

pct create 120 local:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst \
  --hostname immich-dlna-proxy \
  --unprivileged 1 \
  --features nesting=1 \
  --cores 1 --memory 512 --swap 512 \
  --rootfs local-lvm:8 \
  --net0 name=eth0,bridge=vmbr0,ip=dhcp \
  --onboot 1
pct start 120
```

Notes:

- `nesting=1` (the web UI's default for unprivileged containers) lets
  systemd's sandboxing options in the service file below work; without
  it the service may fail to start with `status=226/NAMESPACE`.
- `120` is the container ID - use any free one. Adjust `local-lvm` and
  `vmbr0` to your storage and LAN bridge.
- 1 core and 512 MB RAM are plenty. Memory use grows with library size,
  since the full timeline listing is kept in memory (a few hundred MB for
  very large libraries); raise it if the container gets OOM-killed.
- Size the root disk for the cache: the default `CACHE_MAX_MB` is 2048
  (2 GB), plus some headroom since it's a soft budget. Alternatively
  set `DISABLE_CACHE=true` and skip the cache entirely.
- A DHCP reservation (or a static `ip=192.168.1.50/24,gw=192.168.1.1`)
  keeps the container's address stable, which TVs appreciate.

## 2. Install the binary

Open a shell in the container (`pct enter 120`) and download the latest
release for your architecture (`amd64` on most Proxmox hosts, `arm64`
on ARM boards) from the
[releases page](https://github.com/jastbytes/immich-dlna-proxy/releases):

```bash
apt update && apt install -y curl ca-certificates

VERSION=v1.2.3   # replace with the latest release tag
ARCH=amd64
NAME="immich-dlna-proxy_${VERSION}_linux_${ARCH}"

cd /tmp
curl -fLO "https://github.com/jastbytes/immich-dlna-proxy/releases/download/${VERSION}/${NAME}.tar.gz"
curl -fLO "https://github.com/jastbytes/immich-dlna-proxy/releases/download/${VERSION}/checksums.txt"
sha256sum --check --ignore-missing checksums.txt

tar -xzf "${NAME}.tar.gz"
install -m 0755 "${NAME}/immich-dlna-proxy" /usr/local/bin/immich-dlna-proxy
```

## 3. Configure

Create a dedicated user, the cache directory and an environment file:

```bash
useradd --system --no-create-home --shell /usr/sbin/nologin immich-dlna-proxy
install -d -o immich-dlna-proxy -g immich-dlna-proxy /var/cache/immich-dlna-proxy

install -d /etc/immich-dlna-proxy
cat > /etc/immich-dlna-proxy/env <<'EOF'
IMMICH_URL=http://192.168.1.10:2283
IMMICH_API_KEY=your-api-key
FRIENDLY_NAME=Immich Photos
CACHE_DIR=/var/cache/immich-dlna-proxy
TZ=Europe/Berlin
EOF
chmod 600 /etc/immich-dlna-proxy/env
```

The API key needs the permissions listed under
[API key permissions](configuration.md#api-key-permissions); every other
variable is described in [Configuration](configuration.md#environment-variables).
`CACHE_DIR` must be set here, since the default (`/config/cache`) is
meant for the Docker image.

## 4. Run it as a systemd service

```bash
cat > /etc/systemd/system/immich-dlna-proxy.service <<'EOF'
[Unit]
Description=Immich DLNA proxy
Wants=network-online.target
After=network-online.target

[Service]
User=immich-dlna-proxy
Group=immich-dlna-proxy
EnvironmentFile=/etc/immich-dlna-proxy/env
ExecStart=/usr/local/bin/immich-dlna-proxy
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/cache/immich-dlna-proxy

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now immich-dlna-proxy
```

Check that it started and reaches Immich:

```bash
systemctl status immich-dlna-proxy
journalctl -u immich-dlna-proxy -f
curl -fsS http://localhost:8200/healthz && echo OK
```

The server should now show up on your TVs under `FRIENDLY_NAME` within a
minute or so (or right away when the TV searches for media servers).

## Updating

Repeat step 2 with the new `VERSION`, then:

```bash
systemctl restart immich-dlna-proxy
```

The cache and configuration are kept. Release notes are on the
[releases page](https://github.com/jastbytes/immich-dlna-proxy/releases).

## Alternative: Docker

If you prefer the Docker image, run it in a VM, or in an LXC container
with the *nesting* feature enabled (`pct set 120 --features nesting=1,keyctl=1`,
then install Docker inside). Use host networking, as everywhere else:

```bash
docker run -d \
  --name immich-dlna-proxy \
  --restart unless-stopped \
  --network host \
  -v /opt/immich-dlna-proxy/cache:/config/cache \
  -e IMMICH_URL=http://192.168.1.10:2283 \
  -e IMMICH_API_KEY=your-api-key \
  -e FRIENDLY_NAME="Immich Photos" \
  ghcr.io/jastbytes/immich-dlna-proxy:latest
```

The image runs as UID/GID `65532`, so the host cache directory must be
writable by it (`chown -R 65532:65532 /opt/immich-dlna-proxy/cache`).
See [Running in Docker](configuration.md#running-in-docker) for details.

## Troubleshooting

- **TV doesn't find the server:** check that the container has an
  address on the TVs' LAN (`ip -4 addr` inside the container), that the
  Proxmox firewall allows UDP 1900 and TCP 8200, and that the TV and the
  container are on the same VLAN. Some managed switches with IGMP
  snooping but no IGMP querier drop SSDP multicast after a few minutes -
  enabling a querier (or disabling snooping) fixes that.
- **TV finds the server but can't open it:** the proxy may be announcing
  the wrong address (e.g. with several network interfaces in the
  container). Set `ADVERTISE_IP` to the container's LAN address, or
  `SSDP_INTERFACE=eth0`.
- **Folders load but photos or videos don't:** the API key is probably
  missing a permission - the log (`journalctl -u immich-dlna-proxy`)
  names which one.
- **More detail:** set `DEBUG=true` in `/etc/immich-dlna-proxy/env` and
  restart the service.
