"""Process a single user turn through a provider-independent model gateway."""

import asyncio
import json

from pydantic import ValidationError

from app.core.config import Settings
from app.core.errors import InvalidModelOutput, ServiceError
from app.llm.messages import build_messages
from app.llm.protocols import ModelGateway
from app.llm.schemas import ModelDecision
from app.schemas.delete_event import DeleteCandidateContext
from app.schemas.job import JobRequest, JobResponse
from app.schemas.update_event import UpdateCandidateContext
from app.services.context import build_job_response, explicit_selection_numbers
from app.services.task_deadline import normalize_task_deadline


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
        # The candidate numbers are already trusted dialogue state. Handle an
        # explicit choice before the model can reinterpret it as a new search.
        if isinstance(request.context, (DeleteCandidateContext, UpdateCandidateContext)):
            is_delete = isinstance(request.context, DeleteCandidateContext)
            selected = explicit_selection_numbers(
                request.text, {candidate.number for candidate in request.context.candidates},
                "on_delete" if is_delete else "on_update",
            )
            if selected is not None:
                return build_job_response(request, ModelDecision.model_validate({
                    "status": "ready", "intent": "on_delete" if is_delete else "on_update",
                    "message": "Удаляю выбранные записи." if is_delete else "Выбранные записи изменены.",
                    "action": {
                        "phase": "delete_confirmed" if is_delete else "update_confirmed",
                        "selected_numbers": selected,
                    },
                    "draft": None, "topic_changed": False,
                }))
        messages = build_messages(request, ModelDecision.model_json_schema())
        for attempt in range(2):
            try:
                content = await self.gateway.complete(messages)
                decision = ModelDecision.model_validate_json(content)
                decision = normalize_task_deadline(request, decision)
                if decision.status == "unsupported":
                    raise ServiceError(
                        400,
                        "unsupported_intent",
                        "Unsupported request",
                        "Supported requests create, list, search, update, or delete calendar entries.",
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
