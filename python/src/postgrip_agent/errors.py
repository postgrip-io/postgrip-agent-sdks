class PostGripAgentError(Exception):
    pass


class ApplicationFailure(PostGripAgentError):
    def __init__(self, message: str, *, type: str = "ApplicationFailure", non_retryable: bool = False, details=None):
        super().__init__(message)
        self.type = type
        self.non_retryable = non_retryable
        self.details = list(details or [])

    @classmethod
    def non_retryable_failure(cls, message: str, type: str = "NonRetryableFailure", *details):
        return cls(message, type=type, non_retryable=True, details=details)


class CancelledFailure(PostGripAgentError):
    pass


class TaskFailedError(PostGripAgentError):
    def __init__(self, task_id: str, message: str):
        super().__init__(message)
        self.task_id = task_id


class TimeoutFailure(PostGripAgentError):
    pass

