# Runbook E — Suspected Cross-Tenant Access Incident

## 1. Symptoms & Alerts
- Repeated HTTP 404 responses in API logs with high velocity from a single API key (ID enumeration scan).
- Security alert regarding unauthorized access attempts across tenant boundaries.

## 2. Diagnostic Steps
1. **Analyze Request Logs:**
   Filter logs by the suspicious `api_key_id` or `display_prefix` and extract affected `job_id` or `schedule_id` values.
2. **Verify Object-Level Isolation:**
   Confirm that QueueForge queries strictly enforce tenant scoping:
   ```sql
   SELECT id, tenant_id FROM jobs WHERE id = $1 AND tenant_id = $2;
   ```
   Cross-tenant attempts return a generic `404 Not Found` to prevent resource existence disclosure.
3. **Audit Key Usage:**
   Check `api_keys.last_used_at` and verify if the key was leaked or compromised.

## 3. Resolution & Mitigation
1. **Revoke Compromised API Key Immediately:**
   ```bash
   ./bin/admin revoke-api-key --key-id <KEY_UUID>
   ```
   Or execute directly in the admin CLI.
2. **Issue Replacement API Key:**
   Generate a new API key with least-privilege permissions and securely transmit it to the legitimate tenant owner.
3. **Preserve Audit Trail:**
   Export relevant lines from `job_audit_events` and API access logs for post-incident security reporting.
