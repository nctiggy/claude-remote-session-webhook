# Deployment

**Nothing in this directory contains a real value.** Every file here is an example.
Real hostnames, tunnel IDs, account IDs, usernames, paths, and secrets are supplied
at deploy time from the environment or 1Password.

This repository is public. Treat every file in it as published, because it is.

## What you fill in yourself

| Value | Where it comes from | Never |
|---|---|---|
| Public hostname | Your own DNS zone | In this repo |
| `CRSW_SHARED_SECRET` | 1Password (`Lobster` vault) | In this repo, in a unit file, in a log |
| `CRSW_ACCESS_AUD` | Cloudflare Access → application → AUD tag | In this repo |
| `CRSW_ACCESS_TEAM_DOMAIN` | `https://<team>.cloudflareaccess.com` | In this repo |
| `CRSW_ALLOWED_EMAILS` | The one Google account allowed in | In this repo |
| `CRSW_ALLOWED_ROOTS` | Directories sessions may run in | In this repo |
| Tunnel ID + credentials | `cloudflared tunnel create` | In this repo |

The AUD tag and team domain are not secrets in the cryptographic sense, but they
identify your specific Access application. Keep them out of a public repo anyway —
there is no upside to publishing them.

## Order of operations

1. `cloudflared tunnel create crswd` — note the tunnel ID and credentials path
2. Create the Cloudflare Access application, Google IdP, allowlist your one email
3. Copy the AUD tag from the application's settings
4. Put `CRSW_SHARED_SECRET` in 1Password; generate it with `openssl rand -hex 32`
5. Copy both example files, fill them in **outside the repo**, install them
6. `systemctl --user enable --now crswd` then `systemctl --user enable --now cloudflared`

## Verifying the exposure model

After deploying, confirm the daemon is not reachable except through the tunnel:

```bash
ss -tlnp | grep crswd          # must show 127.0.0.1:PORT, never 0.0.0.0
curl -sS http://<host-lan-ip>:PORT/   # must fail to connect
```

If the second command reaches the daemon, stop and fix the bind address before
going any further. See `docs/security.md`.
