"""Small interface for job-service injection without a provider dependency."""

from typing import Protocol

from app.schemas.job import JobRequest, JobResponse
from app.security.guard_schemas import GuardVerdict


class ActionVerifier(Protocol):
    async def verify(
        self, request: JobRequest, *, intent: str, proposal: JobResponse,
    ) -> GuardVerdict:
        """Check the final public proposal, including code-generated promotions."""
        ...
