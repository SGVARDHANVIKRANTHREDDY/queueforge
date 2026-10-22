class QueueForgeError(Exception):
    """Base exception for all QueueForge errors."""
    def __init__(self, message, code=None, status_code=None, request_id=None):
        super().__init__(message)
        self.code = code
        self.status_code = status_code
        self.request_id = request_id


class AuthenticationError(QueueForgeError):
    """Raised when API key is invalid, missing, or revoked."""
    pass


class PermissionDeniedError(QueueForgeError):
    """Raised when the key lacks necessary permissions."""
    pass


class NotFoundError(QueueForgeError):
    """Raised when the requested job or resource does not exist."""
    pass


class ConflictError(QueueForgeError):
    """Raised when an idempotency key is reused with a different request or state conflict occurs."""
    pass


class IdempotencyConflictError(ConflictError):
    """Specific error when an idempotency key is reused with a different request."""
    pass


class RateLimitError(QueueForgeError):
    """Raised when the tenant enqueue rate limit is exceeded."""
    def __init__(self, message, retry_after=None, **kwargs):
        super().__init__(message, **kwargs)
        self.retry_after = retry_after


class QuotaExceededError(QueueForgeError):
    """Raised when maximum pending jobs or active schedules quota is reached."""
    pass
