# deploy/

Everything this fork adds for running new-api on its own servers. All of it is
new files, so upstream changes to `docker-compose.yml`, `.env.example` or the
Dockerfiles never conflict with a fork-local customization.

| File | Purpose |
|---|---|
| `../docker-compose.override.yml` | Compose loads it automatically next to `docker-compose.yml`; replaces the upstream demo passwords and image with values from `.env`, unpublishes port 3000 and adds Caddy |
| `.env.example` | Template for the repository-root `.env`; copy it and fill in real secrets |
| `deploy.sh` | Builds and releases one git tag on the current host, with a Postgres dump and automatic rollback |
| `caddy/Caddyfile` | TLS termination and reverse proxy; the domain comes from `APP_DOMAIN` so no host-specific value is committed |

## Branch layout

| Branch | Rule |
|---|---|
| `main` | Pure mirror of `upstream/main`. Never commit here — only `git merge --ff-only upstream/main` |
| `deploy` | `main` plus the fork-local patches. Rebase onto `main` when syncing |
| `feat/*` | Branch off `main` for anything meant to go back upstream as a PR |

Sync upstream:

```bash
git fetch upstream
git checkout main && git merge --ff-only upstream/main && git push origin main
git checkout deploy && git rebase main && git push --force-with-lease origin deploy
```

`git log main..deploy` is the complete list of fork-local changes. Keep it short:
the smaller that diff, the cheaper every upstream sync is.

## First run on a new host

```bash
git clone -b deploy <fork-url> /root/new-api && cd /root/new-api
cp deploy/.env.example .env && chmod 600 .env
$EDITOR .env                        # set the required secrets
openssl rand -hex 32                # SESSION_SECRET
git tag -l 'deploy-*' | tail -1     # pick the tag to run
deploy/deploy.sh deploy-YYYYMMDD.N
```

## Releasing

```bash
git checkout deploy
git tag -a deploy-$(date +%Y%m%d).1 -m 'what changed'
git push origin deploy --tags
ssh <host> 'cd /root/new-api && deploy/deploy.sh deploy-YYYYMMDD.1'
```

Rolling back is deploying the previous tag — its image is still on disk, so no
rebuild happens.

## Moving a host onto HTTPS

The topology is browser → Cloudflare → Caddy → app. Caddy serves a **Cloudflare
Origin CA certificate**, not an ACME one: Cloudflare is the only client that
ever reaches this port, and an origin certificate is valid for years with no
renewal job that can fail unattended. Browsers never see it — they get
Cloudflare's edge certificate — so the private CA is not a problem.

Caddy publishes 80/443 and the app's port is bound to `127.0.0.1`, so all of
these steps have to land together or the site becomes unreachable.

1. In Cloudflare DNS, point the A record at the host and keep it **proxied**
   (orange cloud). Delete any AAAA record that still points at Cloudflare.
2. Generate the keypair and CSR **on the host** so the private key never
   travels:

   ```bash
   mkdir -p /etc/new-api/certs && chmod 700 /etc/new-api/certs
   openssl req -new -newkey rsa:2048 -nodes \
     -keyout /etc/new-api/certs/origin.key -out /etc/new-api/certs/origin.csr \
     -subj "/CN=example.com" \
     -addext "subjectAltName=DNS:example.com,DNS:*.example.com"
   chmod 600 /etc/new-api/certs/origin.key
   ```

3. Cloudflare → SSL/TLS → Origin Server → Create Certificate → **Use my private
   key and CSR**. Paste the CSR, list the same hostnames, and save the issued
   PEM to `/etc/new-api/certs/origin.pem`.
4. Cloudflare → SSL/TLS → Overview → encryption mode **Full (strict)**. Anything
   less leaves the Cloudflare-to-origin leg unverified, which defeats the point
   of terminating TLS here at all.
5. Fill in `.env`:

   ```
   APP_DOMAIN=api.example.com
   CADDY_CERT_DIR=/etc/new-api/certs
   SESSION_COOKIE_SECURE=true
   SESSION_COOKIE_TRUSTED_URL=https://api.example.com
   TRUSTED_PROXIES=<compose network subnet>
   ```

   `SESSION_COOKIE_SECURE` without a matching `SESSION_COOKIE_TRUSTED_URL`
   breaks refresh and logout, so never set one without the other. Read the
   subnet with `docker network inspect new-api_new-api-network`.
6. Deploy the tag as usual, then confirm the whole chain before announcing the
   new URL:

   ```bash
   docker compose logs caddy | grep -iE "serving|error"
   curl -sI https://api.example.com/api/status          # through Cloudflare
   curl -sI --resolve api.example.com:443:<host-ip> \
     https://api.example.com/api/status                 # straight at the origin
   ```

Two consequences worth knowing before the switch:

- Any client pinned to `http://<ip>:3000` stops working — update those callers
  in the same change.
- Cloudflare's proxy read timeout is 125 s on every plan below Enterprise. It is
  an *idle* timeout, so streaming responses are fine as long as data keeps
  flowing, but a non-streaming request that thinks for longer than that returns
  524.

The orange cloud hides the origin IP but does not stop anyone who learns it from
connecting directly. Restricting 80/443 to [Cloudflare's published
ranges](https://www.cloudflare.com/ips/) at the firewall is the follow-up that
makes the proxy actually mandatory.
