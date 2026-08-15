#!/usr/bin/env bash
# Build and release one git tag of this fork on the current host.
#
#   deploy/deploy.sh deploy-20260815.1
#
# The image is tagged after the git ref, so rolling back is just deploying the
# previous tag again — its image is still on disk. Postgres is dumped before the
# swap and the previous image is restored if the new container never turns
# healthy.
set -euo pipefail

REF="${1:-}"
if [ -z "$REF" ]; then
  echo "usage: deploy/deploy.sh <git-tag-or-sha>" >&2
  exit 64
fi

cd "$(dirname "$0")/.."
ROOT="$PWD"
REMOTE="${DEPLOY_REMOTE:-origin}"
BACKUP_DIR="${DEPLOY_BACKUP_DIR:-$ROOT/backups}"
HEALTH_TIMEOUT="${DEPLOY_HEALTH_TIMEOUT:-120}"

if [ ! -f .env ]; then
  echo "error: .env missing — copy deploy/.env.example to .env and fill it in" >&2
  exit 1
fi
set -a
# shellcheck disable=SC1091
. ./.env
set +a

git fetch --quiet --tags "$REMOTE"
SHA=$(git rev-parse --verify "${REF}^{commit}")
IMAGE="new-api:$(printf '%s' "$REF" | tr -c 'A-Za-z0-9_.-' '-')"
PREV_IMAGE="${NEW_API_IMAGE:-}"

echo "==> deploying $REF ($(git rev-parse --short "$SHA")) as $IMAGE"
git checkout --quiet --detach "$SHA"

# The Dockerfile bakes VERSION into the binary, so write it for the build and
# restore it afterwards to keep the checkout clean for the next deploy.
printf '%s\n' "$REF" >VERSION
trap 'git -C "$ROOT" checkout -- VERSION 2>/dev/null || true' EXIT
docker build -t "$IMAGE" .
git checkout -- VERSION

echo "==> backing up postgres"
mkdir -p "$BACKUP_DIR"
docker compose exec -T postgres pg_dump -U "${POSTGRES_USER:-root}" "${POSTGRES_DB:-new-api}" \
  | gzip >"$BACKUP_DIR/$(date +%F-%H%M)-$REF.sql.gz"
ls -1t "$BACKUP_DIR"/*.sql.gz 2>/dev/null | tail -n +11 | xargs -r rm --

set_image() {
  # Compose resolves ${NEW_API_IMAGE} from the process environment before it
  # looks at .env, and this script exported the old value when it sourced .env
  # above. Updating only the file would leave the running container on the
  # previous image while every log line claims the new one shipped.
  export NEW_API_IMAGE="$1"
  if grep -q '^NEW_API_IMAGE=' .env; then
    sed -i "s|^NEW_API_IMAGE=.*|NEW_API_IMAGE=$1|" .env
    return
  fi
  printf 'NEW_API_IMAGE=%s\n' "$1" >>.env
}

echo "==> swapping container"
set_image "$IMAGE"
docker compose up -d --no-deps new-api

running=$(docker inspect new-api --format '{{.Config.Image}}' 2>/dev/null || echo none)
if [ "$running" != "$IMAGE" ]; then
  echo "error: container runs $running, expected $IMAGE — the swap did not take effect" >&2
  exit 1
fi

deadline=$((SECONDS + HEALTH_TIMEOUT))
while [ "$SECONDS" -lt "$deadline" ]; do
  state=$(docker inspect new-api --format '{{.State.Health.Status}}' 2>/dev/null || echo unknown)
  if [ "$state" = healthy ]; then
    echo "==> $REF is live and healthy"
    docker images 'new-api' --format '{{.ID}}' | awk '!seen[$0]++' | tail -n +6 | xargs -r docker rmi -- 2>/dev/null || true
    exit 0
  fi
  sleep 5
done

echo "error: container never turned healthy within ${HEALTH_TIMEOUT}s" >&2
if [ -z "$PREV_IMAGE" ]; then
  echo "error: no previous NEW_API_IMAGE recorded, cannot roll back automatically" >&2
  exit 1
fi
echo "==> rolling back to $PREV_IMAGE"
set_image "$PREV_IMAGE"
docker compose up -d --no-deps new-api
exit 1
