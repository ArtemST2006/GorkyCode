"""Process a single user turn through a provider-independent model gateway."""

import asyncio
import json

from pydantic import ValidationError

from app.core.config import Settings
from app.core.errors import InvalidModelOutput, ServiceError
from app.llm.messages import build_messages
from app.llm.protocols import ModelGateway
from app.llm.schemas import ModelDecision
from app.schemas.job import JobRequest, JobResponse
from app.services.context import build_job_response


class JobService:
    def __init__(self, settings: Settings, gateway: ModelGateway):
        self.settings = settings
        self.gateway = gateway

    async def process(self, request: JobRequest) -> JobResponse:
        try:
            async with asyncio.timeout(self.settings.job_timeout_seconds):
                return await self._process(request)
        except TimeoutError:
            raise ServiceError(
                504,
                "job_timeout",
                "Processing timeout",
                "The request exceeded its processing deadline.",
            ) from None

    async def _process(self, request: JobRequest) -> JobResponse:
        messages = build_messages(request, ModelDecision.model_json_schema())
        for attempt in range(2):
            try:
                content = await self.gateway.complete(messages)
                decision = ModelDecision.model_validate_json(content)
                if decision.status == "unsupported":
                    raise ServiceError(
                        400,
                        "unsupported_intent",
                        "Unsupported request",
                        "Supported requests create, list, search, or delete calendar entries.",
                    )
                return build_job_response(request, decision)
            except (ValidationError, InvalidModelOutput) as exc:
                if attempt == 1:
                    raise ServiceError(
                        500,
                        "model_invalid_output",
                        "Invalid model response",
                        "The model could not produce a response matching the service contract.",
                    ) from None
                # Only locations/types are sent back, never provider exception text.
                errors = (
                    [{"location": list(e["loc"]), "type": e["type"]} for e in exc.errors()[:12]]
                    if isinstance(exc, ValidationError)
                    else [{"type": "invalid_completion"}]
                )
                messages = messages + [
                    {
                        "role": "user",
                        "content": "Предыдущий результат не прошел проверку. Повтори извлечение из исходных данных; верни полный JSON строго по схеме. Ошибки: "
                        + json.dumps(errors),
                    }
                ]
        raise RuntimeError("Unreachable correction state")
