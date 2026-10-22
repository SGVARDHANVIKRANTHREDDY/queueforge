# Campus Placement Alert Bot

This real-world example demonstrates a complete Python application integrating with QueueForge to manage college campus recruitment workflows.

## Capabilities Demonstrated
1. **Urgent Notifications (Priority 8):** Pushing interview schedule updates with high priority on the `notifications` queue.
2. **Idempotency Safeguard:** Demonstrating that resubmitting notices with identical idempotency keys returns the existing job without duplicate SMS/Email alerts.
3. **Recurring Cron Schedules:** Automatically registering a daily 9:00 AM placement digest schedule (`0 9 * * *`) in the `Asia/Kolkata` timezone with `catch_up_one` misfire handling.
4. **Queue Observability:** Querying tenant-level statistics for the `notifications` queue.

## Usage
```bash
# Set your API key and QueueForge endpoint
export QF_API_KEY="qf_live_..."
export QF_BASE_URL="http://localhost:8080"

python bot.py
```
