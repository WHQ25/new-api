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

Caddy only publishes 80/443, and the app's own port is bound to `127.0.0.1`, so
these three steps have to happen together or the site becomes unreachable.

1. Point the domain's A record at the host and wait for it to resolve. Caddy
   proves ownership over port 80, so the record must be live *before* the first
   start or the ACME challenge fails and enters a retry backoff.
2. Fill in `.env`:

   ```
   APP_DOMAIN=api.example.com
   ACME_EMAIL=you@example.com
   SESSION_COOKIE_SECURE=true
   SESSION_COOKIE_TRUSTED_URL=https://api.example.com
   TRUSTED_PROXIES=<compose network subnet>
   ```

   `SESSION_COOKIE_SECURE` without a matching `SESSION_COOKIE_TRUSTED_URL`
   breaks refresh and logout, so never set one without the other. Read the
   subnet with `docker network inspect new-api_new-api-network`.
3. Deploy the tag as usual. Confirm the certificate before announcing the new
   URL:

   ```bash
   docker compose logs caddy | grep -i "certificate obtained"
   curl -sI https://api.example.com/api/status
   ```

Any client pinned to `http://<ip>:3000` stops working at this point — update
those callers to the HTTPS URL in the same change.
