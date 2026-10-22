import json
import time
import uuid
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Dict, List, Optional

from .exceptions import (
    AuthenticationError,
    ConflictError,
    IdempotencyConflictError,
    NotFoundError,
    PermissionDeniedError,
    QueueForgeError,
    QuotaExceededError,
    RateLimitError,
)


class QueueForgeClient:
    """Lightweight official Python client for QueueForge."""

    def __init__(self, base_url: str = "http://localhost:8080", api_key: str = "", timeout: float = 30.0):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    def _request(
        self,
        method: str,
        path: str,
        params: Optional[Dict[str, Any]] = None,
        data: Optional[Dict[str, Any]] = None,
        headers: Optional[Dict[str, str]] = None,
    ) -> Dict[str, Any]:
        url = f"{self.base_url}{path}"
        if params:
            filtered_params = {k: v for k, v in params.items() if v is not None}
            if filtered_params:
                url = f"{url}?{urllib.parse.urlencode(filtered_params)}"

        req_headers = {
            "Accept": "application/json",
            "User-Agent": "QueueForge-Python-SDK/1.0",
        }
        if self.api_key:
            req_headers["Authorization"] = f"Bearer {self.api_key}"

        body_bytes = None
        if data is not None:
            req_headers["Content-Type"] = "application/json"
            body_bytes = json.dumps(data).encode("utf-8")

        if headers:
            req_headers.update(headers)

        req = urllib.request.Request(url, data=body_bytes, headers=req_headers, method=method)

        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                resp_bytes = resp.read()
                is_replay = resp.headers.get("Idempotent-Replay") == "true"
                if resp_bytes:
                    res_data = json.loads(resp_bytes.decode("utf-8"))
                    if isinstance(res_data, dict):
                        res_data["_idempotent_replay"] = is_replay
                    return res_data
                return {}
        except urllib.error.HTTPError as e:
            error_body = e.read().decode("utf-8")
            err_code = "unknown_error"
            err_msg = error_body
            req_id = None

            try:
                err_json = json.loads(error_body)
                if "error" in err_json:
                    err_code = err_json["error"].get("code", err_code)
                    err_msg = err_json["error"].get("message", err_msg)
                    req_id = err_json["error"].get("request_id")
            except Exception:
                pass

            if e.code == 401:
                raise AuthenticationError(err_msg, code=err_code, status_code=401, request_id=req_id)
            elif e.code == 403:
                raise PermissionDeniedError(err_msg, code=err_code, status_code=403, request_id=req_id)
            elif e.code == 404:
                raise NotFoundError(err_msg, code=err_code, status_code=404, request_id=req_id)
            elif e.code == 409:
                if err_code == "idempotency_key_reused":
                    raise IdempotencyConflictError(err_msg, code=err_code, status_code=409, request_id=req_id)
                raise ConflictError(err_msg, code=err_code, status_code=409, request_id=req_id)
            elif e.code == 429:
                retry_after = e.headers.get("Retry-After")
                retry_after_sec = float(retry_after) if retry_after else None
                if err_code == "pending_quota_exceeded" or err_code == "schedule_quota_exceeded":
                    raise QuotaExceededError(err_msg, code=err_code, status_code=429, request_id=req_id)
                raise RateLimitError(err_msg, code=err_code, status_code=429, request_id=req_id, retry_after=retry_after_sec)
            else:
                raise QueueForgeError(err_msg, code=err_code, status_code=e.code, request_id=req_id)
        except Exception as e:
            raise QueueForgeError(f"Network error communicating with QueueForge: {e}")

    def submit_job(
        self,
        task_type: str,
        payload: Dict[str, Any],
        queue: str = "default",
        priority: int = 5,
        run_at: Optional[str] = None,
        max_attempts: int = 5,
        timeout_seconds: int = 300,
        idempotency_key: Optional[str] = None,
        max_retries: int = 2,
    ) -> Dict[str, Any]:
        """Submits a job to QueueForge.

        An Idempotency-Key is ALWAYS sent: yours if you pass one, otherwise a random one generated
        for this call. Retries (network errors, 5xx, 429) reuse that same key, so a response lost
        after the server committed the job cannot create a duplicate. Pass your own key if you also
        want separate calls (for example after a process restart) to be deduplicated.
        """
        headers = {"Idempotency-Key": idempotency_key or ("py-sdk-" + uuid.uuid4().hex)}

        data = {
            "task_type": task_type,
            "queue": queue,
            "payload": payload,
            "priority": priority,
            "max_attempts": max_attempts,
            "timeout_seconds": timeout_seconds,
        }
        if run_at:
            data["run_at"] = run_at
        return self._post_with_retries("/v1/jobs", data, headers, max_retries)

    def submit_batch(
        self,
        jobs: List[Dict[str, Any]],
        idempotency_key: Optional[str] = None,
        max_retries: int = 2,
    ) -> Dict[str, Any]:
        """Submits up to 100 jobs atomically (all accepted or none) in one request.

        Each entry is a dict like {"task_type": "...", "payload": {...}} and may include queue,
        priority, max_attempts, timeout_seconds, run_at. Retries reuse one batch-level idempotency
        key, so a retried batch never creates duplicates.
        """
        if not jobs or len(jobs) > 100:
            raise ValueError("a batch must contain between 1 and 100 jobs")
        headers = {"Idempotency-Key": idempotency_key or ("py-sdk-batch-" + uuid.uuid4().hex)}
        return self._post_with_retries("/v1/jobs/batch", {"jobs": jobs}, headers, max_retries)

    def _post_with_retries(
        self, path: str, data: Dict[str, Any], headers: Dict[str, str], max_retries: int
    ) -> Dict[str, Any]:
        attempts = 0
        while True:
            attempts += 1
            try:
                return self._request("POST", path, data=data, headers=headers)
            except RateLimitError as e:
                if attempts <= max_retries and e.retry_after:
                    time.sleep(min(e.retry_after, 5.0))
                    continue
                raise
            except QueueForgeError as e:
                # Do not retry 4xx errors
                if e.status_code and 400 <= e.status_code < 500:
                    raise
                if attempts <= max_retries:
                    time.sleep(0.5 * (2 ** (attempts - 1)))
                    continue
                raise

    def get_job(self, job_id: str) -> Dict[str, Any]:
        return self._request("GET", f"/v1/jobs/{job_id}")

    def list_jobs(
        self,
        status: Optional[str] = None,
        queue: Optional[str] = None,
        task_type: Optional[str] = None,
        limit: int = 50,
        cursor: Optional[str] = None,
    ) -> Dict[str, Any]:
        params = {
            "status": status,
            "queue": queue,
            "task_type": task_type,
            "limit": limit,
            "cursor": cursor,
        }
        return self._request("GET", "/v1/jobs", params=params)

    def cancel_job(self, job_id: str) -> Dict[str, Any]:
        return self._request("POST", f"/v1/jobs/{job_id}/cancel")

    def replay_job(self, job_id: str, reason: str = "Manual SDK replay") -> Dict[str, Any]:
        return self._request("POST", f"/v1/jobs/{job_id}/replay", data={"reason": reason})

    def list_dead_letters(self, queue: Optional[str] = None, limit: int = 50) -> Dict[str, Any]:
        return self._request("GET", "/v1/dead-letters", params={"queue": queue, "limit": limit})

    def create_schedule(
        self,
        name: str,
        task_type: str,
        payload: Dict[str, Any],
        cron_expression: str,
        timezone: str = "UTC",
        queue: str = "default",
        priority: int = 5,
        max_attempts: int = 5,
        timeout_seconds: int = 300,
        misfire_policy: str = "skip",
    ) -> Dict[str, Any]:
        data = {
            "name": name,
            "task_type": task_type,
            "payload": payload,
            "cron_expression": cron_expression,
            "timezone": timezone,
            "queue": queue,
            "priority": priority,
            "max_attempts": max_attempts,
            "timeout_seconds": timeout_seconds,
            "misfire_policy": misfire_policy,
        }
        return self._request("POST", "/v1/schedules", data=data)

    def list_schedules(self) -> Dict[str, Any]:
        return self._request("GET", "/v1/schedules")

    def get_schedule(self, schedule_id: str) -> Dict[str, Any]:
        return self._request("GET", f"/v1/schedules/{schedule_id}")

    def pause_schedule(self, schedule_id: str, paused: bool = True) -> Dict[str, Any]:
        return self._request("PATCH", f"/v1/schedules/{schedule_id}", data={"paused": paused})

    def delete_schedule(self, schedule_id: str) -> Dict[str, Any]:
        return self._request("DELETE", f"/v1/schedules/{schedule_id}")

    def get_queue_stats(self, queue: str = "default") -> Dict[str, Any]:
        return self._request("GET", f"/v1/queues/{queue}/stats")

    def poll_until_complete(
        self,
        job_id: str,
        poll_interval: float = 0.5,
        timeout: float = 60.0,
    ) -> Dict[str, Any]:
        """Polls until job reaches terminal state ('succeeded', 'dead', 'cancelled')."""
        start = time.time()
        while time.time() - start < timeout:
            job = self.get_job(job_id)
            status = job.get("status")
            if status in ("succeeded", "dead", "cancelled"):
                return job
            time.sleep(poll_interval)
        raise TimeoutError(f"Job {job_id} did not complete within {timeout} seconds")
