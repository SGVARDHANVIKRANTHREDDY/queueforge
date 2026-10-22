#!/usr/bin/env bash
# Nightly logical backup. Cron example (as the deploy user):
#   15 2 * * * cd /opt/queueforge && ./deploy/backup.sh >> /var/log/qf-backup.log 2>&1
# A backup you have never restored is a hope, not a backup: run deploy/restore-drill.sh monthly.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT="${BACKUP_DIR:-/var/backups/queueforge}"; KEEP_DAYS="${KEEP_DAYS:-14}"
mkdir -p "$OUT"; f="$OUT/qf-$(date +%Y%m%d-%H%M%S).sql.gz"
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod exec -T postgres \
  pg_dump -U queueforge -d queueforge --no-owner | gzip -9 > "$f"
[ -s "$f" ] || { echo "empty backup!"; rm -f "$f"; exit 1; }
find "$OUT" -name 'qf-*.sql.gz' -mtime +"$KEEP_DAYS" -delete
echo "$(date -Is) backup ok: $f ($(du -h "$f" | cut -f1))"
# Copy OFF the machine too (a backup on the same disk dies with the disk), e.g.:
#   rclone copy "$f" remote:queueforge-backups/    # or: aws s3 cp "$f" s3://my-bucket/
