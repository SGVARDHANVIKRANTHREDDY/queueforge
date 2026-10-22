# Deploying QueueForge on a Single VPS

Goal: a public HTTPS URL, durable data, backups you have actually restored, and metrics you can look at, on one small machine. This is the cheapest path to the thing that matters most for your resume: **real usage**.

> **Status of this guide:** the application, smoke test, and metrics endpoints were run and verified against real processes. The Docker/Caddy files are syntax-checked but **were not executed** in the authoring sandbox (no Docker there). Treat your first deploy as the test, and fix this guide with whatever you learn. That is itself good engineering-log material.

## 0. What you get

```
Internet --443--> Caddy (auto-TLS) --> api:8080 --> Postgres (private)
                                       worker x N ------^
                  Prometheus (127.0.0.1 only; use an SSH tunnel)
```
Only ports 22, 80 and 443 are open. Postgres, the API's ops listener (`:9090`) and Prometheus are never public.

## 1. Pick a machine
- 2 vCPU / 4 GB RAM / 40 GB disk, Ubuntu 22.04 or 24.04 is plenty for thousands of jobs per minute.
- Any provider works (DigitalOcean/Hetzner/Lightsail/Oracle free tier/GCP/AWS credits). Check current free-tier terms yourself.
- A domain you control (a cheap one, or a free subdomain service) pointing an **A record** at the server's IP.

## 2. Harden the box (15 minutes)
```bash
adduser deploy && usermod -aG sudo deploy        # work as a non-root user
# copy your SSH public key to /home/deploy/.ssh/authorized_keys, then in /etc/ssh/sshd_config:
#   PermitRootLogin no   /   PasswordAuthentication no     (then: systemctl restart ssh)
ufw default deny incoming && ufw allow 22/tcp && ufw allow 80/tcp && ufw allow 443/tcp && ufw enable
apt update && apt install -y unattended-upgrades fail2ban && dpkg-reconfigure -plow unattended-upgrades
curl -fsSL https://get.docker.com | sh && usermod -aG docker deploy
```
> Docker publishes ports by editing iptables and **bypasses ufw**. That is why the compose file publishes only Caddy's 80/443 and binds Prometheus to loopback. Never add `ports:` to the postgres service.

## 3. Configure and start
```bash
sudo mkdir -p /opt/queueforge && sudo chown deploy: /opt/queueforge && cd /opt/queueforge
git clone <your-repo-url> . 
cp deploy/.env.prod.example deploy/.env.prod && chmod 600 deploy/.env.prod
# edit: DOMAIN, POSTGRES_PASSWORD (openssl rand -base64 32 | tr -d '/+=' | cut -c1-32), QF_ALLOWED_ORIGINS
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build --scale worker=2
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod ps     # everything "healthy"
```
Migrations run automatically when the API starts. Caddy fetches the certificate on first request (needs DNS correct and 80/443 reachable; check `docker compose logs caddy`).

Shortcut for the commands below:
```bash
alias qf='docker compose -f /opt/queueforge/deploy/docker-compose.prod.yml --env-file /opt/queueforge/deploy/.env.prod'
```

**Optional but recommended: least-privilege database roles.** After the first start (which runs migrations), run `deploy/sql/least_privilege.sql` as a superuser, then set `QF_DATABASE_URL` to the `qf_app` role and `QF_MIGRATE_DATABASE_URL` to the `qf_migrate` role. Not yet exercised against a real database; run the smoke test afterwards.

## 4. Create your first tenant and API key
```bash
qf exec api /app/admin create-tenant -name "my-college-club" -rate 20 -burst 50 -pending 5000 -concurrent 4
qf exec api /app/admin create-api-key -tenant-id <TENANT_UUID> -name "club-bot"
```
The secret (`qf_live_...`) is shown **once**. Store it in a password manager. Give each real user their own tenant so quotas and isolation apply.

## 5. Smoke test (run after EVERY deploy)
```bash
BASE_URL=https://$DOMAIN API_KEY=qf_live_xxx ./deploy/smoke.sh
qf exec worker wget -qO- http://127.0.0.1:9090/metrics | grep queueforge_jobs_claimed_total
```
It checks health, readiness, auth rejection, that `/metrics` is not public, enqueue, idempotent replay (200) vs conflict (409), that a worker actually executes the job, and the dead-letter and worker endpoints. Expect `failed=0`.

## 6. Look at your system
```bash
ssh -L 9090:127.0.0.1:9090 deploy@your-server     # then open http://localhost:9090
```
Useful queries: `rate(queueforge_jobs_claimed_total[1m])`, `histogram_quantile(0.95, rate(queueforge_job_queue_wait_seconds_bucket[5m]))`, `sum(queueforge_jobs_queued)`. The Grafana dashboard JSON in `deploy/grafana/` can be added later; **take screenshots** for your README.

## 7. Monitoring and alerts (minimum viable)
1. **Uptime:** a free external monitor (UptimeRobot, Better Stack) on `https://$DOMAIN/readyz` with email/Telegram alerts.
2. **Heartbeat canary:** a cron on another machine that submits an `echo` job every 5 minutes and alerts if it does not reach `succeeded` within 60 s. That catches "API up, workers dead", which `/readyz` cannot.
3. Follow `docs/runbooks/` when an alert fires, and **write down every incident** (see section 11).

## 8. Backups (and the drill that makes them real)
```bash
crontab -e     # 15 2 * * * cd /opt/queueforge && ./deploy/backup.sh >> /var/log/qf-backup.log 2>&1
./deploy/backup.sh && ./deploy/restore-drill.sh      # run once now, then monthly
```
Also copy dumps **off the machine** (`rclone`/`aws s3 cp`). Logical dumps give you RPO of up to 24 h; if that is too much, move to a managed Postgres with point-in-time recovery.

## 9. Upgrade and rollback
```bash
cd /opt/queueforge && git pull && ./deploy/backup.sh
qf up -d --build --scale worker=2        # workers get SIGTERM, drain up to 30 s, then restart
BASE_URL=https://$DOMAIN API_KEY=qf_live_xxx ./deploy/smoke.sh
```
Rollback code: `git checkout <previous-tag> && qf up -d --build`. **Migrations are forward-only**: before shipping a schema change, make it backward compatible (add columns/tables first, remove in a later release) so the previous version still runs against the new schema.

## 10. Production checklist (tick before telling anyone it is "live")
- [ ] HTTPS works; `http://` redirects; `curl -I https://$DOMAIN/metrics` returns 404
- [ ] Postgres has no published port: `ss -ltnp | grep 5432` shows nothing on a public address
- [ ] `.env.prod` is `chmod 600`, not in git; default passwords replaced
- [ ] API keys are per-tenant, with the minimum permissions needed
- [ ] `QF_ALLOWED_ORIGINS` is your domain, not `*`
- [ ] Smoke test passes from outside the server
- [ ] Backup ran; **restore drill passed**
- [ ] External uptime monitor + canary job alerting to your phone
- [ ] You killed a worker container on purpose (`docker kill`) and watched the job get recovered
- [ ] You know how to read `qf logs -f worker` and what a lease recovery looks like

## 11. Operating it like an engineer (this is what interviews reward)
Keep `docs/OPERATIONS_LOG.md`: date, what happened, how you detected it, impact, root cause, fix, what you changed so it cannot recur. One real entry ("worker OOM-killed at 3 AM; added memory limit and a canary") is worth more than a page of architecture. Amazon calls the formal version a Correction of Error (COE); write one.

## Known limitations of this deployment
- Single node = single point of failure (Postgres, API, Caddy). Recovery is "restore from backup", not failover.
- The database connection uses `sslmode=disable` because it is on the private Docker network. If Postgres moves to another host, switch the `sslmode` in `QF_DATABASE_URL` to `verify-full` (the connection string is the only TLS setting; an earlier `QF_DATABASE_TLS_MODE` variable did nothing and was removed).
- No log shipping: logs live in Docker's local json-file driver. Set `max-size`/`max-file` limits or ship them somewhere.
- Ops endpoints have no authentication; they rely on never being published.
