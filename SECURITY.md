# Security Policy

## Supported versions

Only the latest release is supported. Please upgrade before reporting an
issue if you're not on the latest tagged version.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities.

Instead, use [GitHub's private vulnerability reporting](../../security/advisories/new)
for this repository (Security tab → "Report a vulnerability"). This lets us
discuss and fix the issue before it's public.

You should get an initial response within a few days. This is a
single-maintainer hobby project, so please be patient — there's no SLA.

## Scope and known tradeoffs

A few things are by design, not oversights, and don't need a report:

- **No authentication on the DLNA/HTTP endpoints.** Like most DLNA/UPnP
  servers (e.g. minidlna), anyone who can reach `LISTEN_ADDR` on your
  network can browse and download the photos this proxy exposes, and
  anyone who can reach UDP 1900 can see it advertised via SSDP. Don't
  expose it beyond a trusted LAN; there's no plan to add auth, since DLNA
  clients (TVs) generally can't do auth challenges anyway.
- **The disk cache under `CACHE_DIR` is plaintext**, protected only by
  filesystem permissions — see `docs/configuration.md`.
- **`IMMICH_API_KEY` is read from an environment variable**, not a secrets
  manager. Scope the key to the minimum permissions documented in
  `docs/configuration.md` (`album.read` / `asset.read` / `asset.download` /
  `person.read`).

Reports about the above are welcome as documentation improvements, but
won't be treated as vulnerabilities on their own — genuine vulnerabilities
(e.g. path traversal, SSRF via `IMMICH_URL` handling, request smuggling in
the SOAP/HTTP handlers) are very much in scope.
