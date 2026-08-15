# deploy/

Everything this fork adds for running new-api on its own servers. All of it is
new files, so upstream changes to `docker-compose.yml`, `.env.example` or the
Dockerfiles never conflict with a fork-local customization.

| File | Purpose |
|---|---|
| `../docker-compose.override.yml` | Compose loads it automatically next to `docker-compose.yml`; replaces the upstream demo passwords and image with values from `.env` |
| `.env.example` | Template for the repository-root `.env`; copy it and fill in real secrets |
| `deploy.sh` | Builds and releases one git tag on the current host, with a Postgres dump and automatic rollback |

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
