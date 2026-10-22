import io
import json
import os
import sys
import unittest
import urllib.error
from unittest.mock import patch, MagicMock

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from queueforge import QueueForgeClient
from queueforge.exceptions import (
    AuthenticationError,
    ConflictError,
    IdempotencyConflictError,
    NotFoundError,
    RateLimitError,
)

class TestQueueForgeClient(unittest.TestCase):
    def setUp(self):
        self.client = QueueForgeClient(base_url="http://localhost:8080", api_key="qf_live_test_key_1234567890123456")

    @patch("urllib.request.urlopen")
    def test_submit_job_success(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 202
        mock_resp.headers = {}
        mock_resp.read.return_value = json.dumps({
            "id": "11111111-2222-3333-4444-555555555555",
            "status": "queued",
            "status_url": "/v1/jobs/11111111-2222-3333-4444-555555555555",
        }).encode("utf-8")
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        res = self.client.submit_job(
            task_type="test.task",
            queue="default",
            payload={"key": "value"},
            idempotency_key="key_001",
        )

        self.assertEqual(res["id"], "11111111-2222-3333-4444-555555555555")
        self.assertEqual(res["status"], "queued")
        self.assertFalse(res.get("_idempotent_replay", False))

    @patch("urllib.request.urlopen")
    def test_idempotent_replay(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.headers = {"Idempotent-Replay": "true"}
        mock_resp.read.return_value = json.dumps({
            "id": "11111111-2222-3333-4444-555555555555",
            "status": "queued",
        }).encode("utf-8")
        mock_urlopen.return_value.__enter__.return_value = mock_resp

        res = self.client.submit_job(
            task_type="test.task",
            payload={"key": "value"},
            idempotency_key="key_001",
        )

        self.assertEqual(res["id"], "11111111-2222-3333-4444-555555555555")
        self.assertTrue(res.get("_idempotent_replay"))

    @patch("urllib.request.urlopen")
    def test_idempotency_conflict_error(self, mock_urlopen):
        err_body = json.dumps({
            "error": {
                "code": "idempotency_key_reused",
                "message": "Key used with different request",
            }
        }).encode("utf-8")
        http_err = urllib.error.HTTPError(
            url="http://localhost:8080/v1/jobs",
            code=409,
            msg="Conflict",
            hdrs={},
            fp=io.BytesIO(err_body),
        )
        mock_urlopen.side_effect = http_err

        with self.assertRaises(IdempotencyConflictError):
            self.client.submit_job(
                task_type="test.task",
                payload={"different": "payload"},
                idempotency_key="key_001",
            )

    @patch("urllib.request.urlopen")
    def test_rate_limit_error(self, mock_urlopen):
        err_body = json.dumps({
            "error": {
                "code": "rate_limited",
                "message": "Bucket exhausted",
            }
        }).encode("utf-8")
        http_err = urllib.error.HTTPError(
            url="http://localhost:8080/v1/jobs",
            code=429,
            msg="Too Many Requests",
            hdrs={"Retry-After": "2"},
            fp=io.BytesIO(err_body),
        )
        mock_urlopen.side_effect = http_err

        with self.assertRaises(RateLimitError):
            self.client.submit_job(task_type="test.task", payload={})


class TestIdempotencyDefaults(unittest.TestCase):
    """Regression: submit_job used to send NO Idempotency-Key unless the caller passed one, so a
    retry after a lost response created a duplicate job."""

    def setUp(self):
        self.client = QueueForgeClient(base_url="http://localhost:8080", api_key="qf_live_test_key_1234567890123456")

    @staticmethod
    def _ok_response():
        resp = MagicMock()
        resp.headers = {}
        resp.read.return_value = json.dumps({"id": "11111111-2222-3333-4444-555555555555"}).encode()
        resp.__enter__ = lambda self_: self_
        resp.__exit__ = lambda *a: False
        return resp

    @patch("time.sleep", lambda *_: None)
    @patch("urllib.request.urlopen")
    def test_retry_reuses_an_automatically_generated_key(self, mock_urlopen):
        err = urllib.error.HTTPError("http://x", 503, "unavailable", {}, io.BytesIO(b'{"error":{"code":"x","message":"down"}}'))
        mock_urlopen.side_effect = [err, self._ok_response()]
        self.client.submit_job("echo", {"a": 1})
        self.assertEqual(mock_urlopen.call_count, 2)
        keys = [c.args[0].get_header("Idempotency-key") for c in mock_urlopen.call_args_list]
        self.assertTrue(keys[0] and keys[0] == keys[1], keys)

    @patch("urllib.request.urlopen")
    def test_two_separate_calls_use_different_keys(self, mock_urlopen):
        mock_urlopen.side_effect = [self._ok_response(), self._ok_response()]
        self.client.submit_job("echo", {"a": 1})
        self.client.submit_job("echo", {"a": 1})
        keys = [c.args[0].get_header("Idempotency-key") for c in mock_urlopen.call_args_list]
        self.assertNotEqual(keys[0], keys[1])

    @patch("urllib.request.urlopen")
    def test_batch_posts_to_batch_endpoint_with_key(self, mock_urlopen):
        mock_urlopen.side_effect = [self._ok_response()]
        self.client.submit_batch([{"task_type": "echo", "payload": {}}])
        req = mock_urlopen.call_args.args[0]
        self.assertTrue(req.full_url.endswith("/v1/jobs/batch"))
        self.assertTrue(req.get_header("Idempotency-key"))

    def test_batch_size_is_validated_client_side(self):
        with self.assertRaises(ValueError):
            self.client.submit_batch([])
        with self.assertRaises(ValueError):
            self.client.submit_batch([{"task_type": "x"}] * 101)


if __name__ == "__main__":
    unittest.main()
