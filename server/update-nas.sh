#!/bin/bash
# One-click Synology DSM Task Scheduler update script for Digital Photo Frame.
# Usage in DSM Web UI:
#   Control Panel > Task Scheduler > Create > Scheduled Task > User-defined script
#   User: root | Uncheck "Enabled" (run on demand via "Run" button)
#   Run command: bash /volume1/docker/photoframe/update-nas.sh

set -euo pipefail

# Synology Task Scheduler uses a minimal PATH; include Container Manager & Git paths.
export PATH="/usr/local/bin:/var/packages/ContainerManager/target/usr/bin:/var/packages/Git/target/bin:/usr/sbin:/usr/bin:/sbin:/bin:${PATH:-}"

STACK_DIR="${STACK_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"
REPO_DIR="${REPO_DIR:-${STACK_DIR}/self-host-digital-photo-frame}"

cd "${STACK_DIR}"
echo "[$(date '+%Y-%m-%d %H:%M:%S')] Updating photoframe stack in ${STACK_DIR}"

if [[ -d "${REPO_DIR}/.git" ]]; then
	chmod -R u+rwX "${REPO_DIR}/.git" 2>/dev/null || true
	echo "-> Pulling latest git commits in ${REPO_DIR}..."
	git -c safe.directory="${REPO_DIR}" -C "${REPO_DIR}" pull --ff-only
	echo "-> Latest commit: $(git -c safe.directory="${REPO_DIR}" -C "${REPO_DIR}" log -1 --oneline)"

	echo "-> Syncing server/ files (preserving .env, config.js, and data/)..."
	rsync -av \
		--exclude='.env' \
		--exclude='magicmirror/config/config.js' \
		--exclude='data/' \
		--exclude='test-photos/' \
		--exclude='self-host-digital-photo-frame/' \
		"${REPO_DIR}/server/" "${STACK_DIR}/"
elif git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	echo "-> Pulling latest git commits..."
	git pull --ff-only
	echo "-> Latest commit: $(git log -1 --oneline)"
else
	echo "ERROR: No git repository found at ${REPO_DIR} or ${STACK_DIR}" >&2
	exit 1
fi

# Keep synced files editable in DSM File Station when Task Scheduler runs as root.
if [[ -f "${STACK_DIR}/.env" ]]; then
	PUID_VAL="$(grep -E '^PUID=' "${STACK_DIR}/.env" | tail -n1 | cut -d= -f2 | tr -d '[:space:]"'\''' || true)"
	PGID_VAL="$(grep -E '^PGID=' "${STACK_DIR}/.env" | tail -n1 | cut -d= -f2 | tr -d '[:space:]"'\''' || true)"
	if [[ "${PUID_VAL:-}" =~ ^[0-9]+$ && "${PGID_VAL:-}" =~ ^[0-9]+$ ]]; then
		chown -R "${PUID_VAL}:${PGID_VAL}" "${STACK_DIR}/photo-api" "${STACK_DIR}/magicmirror" "${STACK_DIR}/docker-compose.yml" "${STACK_DIR}/update-nas.sh" "${REPO_DIR}" 2>/dev/null || true
	fi
fi

PROJECT_NAME="${COMPOSE_PROJECT_NAME:-$(basename "${STACK_DIR}" | tr '[:upper:]' '[:lower:]')}"

echo "-> Building Docker images..."
docker compose -p "${PROJECT_NAME}" build

# If an existing container with the same fixed container_name belongs to a different
# Compose project (for example created earlier under a different name in Container Manager),
# remove it so `docker compose up -d` never fails with a name conflict.
for cname in photo-api magicmirror; do
	if docker inspect "${cname}" >/dev/null 2>&1; then
		existing_proj="$(docker inspect -f '{{ index .Config.Labels "com.docker.compose.project" }}' "${cname}" 2>/dev/null || true)"
		if [[ "${existing_proj}" != "${PROJECT_NAME}" ]]; then
			echo "-> Replacing ${cname} from previous project '${existing_proj:-none}'..."
			docker rm -f "${cname}"
		fi
	fi
done

echo "-> Starting Docker Compose services (project: ${PROJECT_NAME})..."
docker compose -p "${PROJECT_NAME}" up -d --remove-orphans

echo "-> Current container status:"
docker compose -p "${PROJECT_NAME}" ps
echo "[$(date '+%Y-%m-%d %H:%M:%S')] Update complete!"
