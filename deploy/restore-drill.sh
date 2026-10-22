#!/usr/bin/env bash
# Restores the newest backup into a THROWAWAY database and checks it is usable.
# Usage: ./deploy/restore-drill.sh [backup-file]
set -euo pipefail
cd "$(dirname "$0")/.."
OUT="${BACKUP_DIR:-/var/backups/queueforge}"
f="${1:-$(ls -1t "$OUT"/qf-*.sql.gz | head -1)}"; db="qf_restore_drill"
dc=(docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod exec -T postgres)
"${dc[@]}" psql -U queueforge -d postgres -qc "DROP DATABASE IF EXISTS $db" -c "CREATE DATABASE $db"
gunzip -c "$f" | "${dc[@]}" psql -U queueforge -d "$db" -q -v ON_ERROR_STOP=1 >/dev/null
jobs=$("${dc[@]}" psql -U queueforge -d "$db" -Atc "SELECT count(*) FROM jobs")
tenants=$("${dc[@]}" psql -U queueforge -d "$db" -Atc "SELECT count(*) FROM tenants")
"${dc[@]}" psql -U queueforge -d postgres -qc "DROP DATABASE $db"
echo "restore drill OK from $f: tenants=$tenants jobs=$jobs"
