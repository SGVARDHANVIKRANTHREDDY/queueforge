"""
Campus Placement Alert Bot
Real-world application using QueueForge to coordinate student placement alerts,
scheduled interview digests, and batch interview reminders.
"""

import os
import sys
import time

if sys.stdout.encoding and sys.stdout.encoding.lower() != 'utf-8':
    try:
        sys.stdout.reconfigure(encoding='utf-8')
    except Exception:
        pass

# Ensure local SDK is imported
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", "sdk", "python")))

from queueforge import QueueForgeClient, QueueForgeError


def run_placement_bot():
    base_url = os.environ.get("QF_BASE_URL", "http://localhost:8080")
    api_key = os.environ.get("QF_API_KEY", "")

    if not api_key:
        print("[!] Warning: QF_API_KEY not set. Set QF_API_KEY to run against live QueueForge.")
        return

    client = QueueForgeClient(base_url=base_url, api_key=api_key)

    print("=" * 60)
    print("Campus Placement Alert Bot (QueueForge Integration)")
    print("=" * 60)

    # 1. Enqueue an urgent drive announcement with high priority (Priority 8)
    notice_id = f"notice_amazon_{int(time.time())}"
    print(f"\n[1] Submitting high-priority placement drive notice ({notice_id})...")
    res = client.submit_job(
        task_type="placement_notice.send",
        queue="notifications",
        priority=8,
        payload={
            "notice_id": notice_id,
            "company": "Amazon AWS",
            "role": "Software Development Engineer",
            "recipient_group": "cs_2028_final_year",
            "message": "AWS Campus Hiring Assessment is scheduled for tomorrow at 10:00 AM.",
        },
        idempotency_key=f"idem_{notice_id}",
    )
    job_id = res["id"]
    print(f" -> Enqueued Job ID: {job_id} (Status: {res['status']})")

    # 2. Re-submit the exact same announcement to demonstrate Idempotency
    print("\n[2] Demonstrating Idempotent submission safeguard...")
    repeat_res = client.submit_job(
        task_type="placement_notice.send",
        queue="notifications",
        priority=8,
        payload={
            "notice_id": notice_id,
            "company": "Amazon AWS",
            "role": "Software Development Engineer",
            "recipient_group": "cs_2028_final_year",
            "message": "AWS Campus Hiring Assessment is scheduled for tomorrow at 10:00 AM.",
        },
        idempotency_key=f"idem_{notice_id}",
    )
    print(f" -> Repeat Submission returned Job ID: {repeat_res['id']} (Replay Header: {repeat_res.get('_idempotent_replay')})")
    assert repeat_res["id"] == job_id, "Idempotency failed: generated different job ID!"

    # 3. Poll until job completes
    print("\n[3] Waiting for worker to complete the notification job...")
    completed_job = client.poll_until_complete(job_id, poll_interval=0.5, timeout=15)
    print(f" -> Job finished with Status: {completed_job['status']}")
    print(f" -> Result: {completed_job.get('result')}")

    # 4. Schedule daily morning placement digest (Cron)
    print("\n[4] Creating daily recurring placement digest schedule (9:00 AM daily)...")
    try:
        schedule = client.create_schedule(
            name="Daily 9AM Placement Digest",
            task_type="placement_notice.send",
            queue="notifications",
            cron_expression="0 9 * * *",
            timezone="Asia/Kolkata",
            payload={
                "type": "daily_summary",
                "recipient_group": "all_registered_students",
            },
            misfire_policy="catch_up_one",
        )
        print(f" -> Created Schedule: {schedule.get('name')} (Next run: {schedule.get('next_run_at')})")
    except QueueForgeError as e:
        print(f" -> Schedule note: {e}")

    # 5. Query queue statistics
    print("\n[5] Fetching operational queue statistics...")
    try:
        stats = client.get_queue_stats("notifications")
        print(f" -> Queue 'notifications' Stats: {stats}")
    except QueueForgeError as e:
        print(f" -> Stats query error: {e}")

    print("\nCampus Placement Alert Bot demonstration complete!")


if __name__ == "__main__":
    run_placement_bot()
