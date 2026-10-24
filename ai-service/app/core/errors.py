"""Safe application errors; provider response bodies must not reach clients."""


class ServiceError(Exception):
    def __init__(self, status_code: int, code: str, title: str, detail: str) -> None:
        self.status_code = status_code
        self.code = code
        self.title = title
        self.detail = detail
        super().__init__(detail)


class InvalidModelOutput(Exception):
    """An unusable completion which may be repaired once by the processor."""
