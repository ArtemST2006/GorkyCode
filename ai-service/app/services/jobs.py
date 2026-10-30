"""Process a single user turn through a provider-independent model gateway."""

import asyncio
import json

from pydantic import ValidationError

from app.core.config import Settings
from app.core.errors import InvalidModelOutput, ServiceError
from app.core.logging import logger
from app.llm.messages import build_messages
from app.llm.protocols import ModelGateway
from app.llm.schemas import ModelDecision
from app.schemas.delete_event import DeleteCandidateContext
from app.schemas.job import JobRequest, JobResponse
from app.schemas.update_event import UpdateCandidateContext
from app.services.context import build_job_response, explicit_selection_numbers
from app.services.task_deadline import normalize_task_deadline
from app.services.list_period import is_orphan_reply
from app.services.status_update_recovery import recover_task_status_update


class JobService:
    def __init__(self, settings: Settings, gateway: ModelGateway):
        self.settings = settings
        self.gateway = gateway

    async def process(self, request: JobRequest) -> JobResponse:
        logger.info(
            "job_started task_id=%s context_messages=%d context_candidates=%d",
            request.task_id, len(getattr(request.context, "recent_messages", [])),
            len(getattr(request.context, "candidates", [])),
        )
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

    def _response(self, request: JobRequest, decision: ModelDecision, source: str) -> JobResponse:
        result = build_job_response(request, decision)
        logger.info(
            "job_completed task_id=%s status=%s intent=%s topic_changed=%s source=%s",
            request.task_id, result.status, decision.intent, decision.topic_changed, source,
        )
        return result

    async def _process(self, request: JobRequest) -> JobResponse:
        if is_orphan_reply(request):
            logger.info("job_rejected task_id=%s code=dialogue_context_required", request.task_id)
            raise ServiceError(
                400, "dialogue_context_required", "Dialogue context required",
                "A short reply alone does not specify an action. Repeat the full request or send the saved dialogue context.",
            )
        # The candidate numbers are already trusted dialogue state. Handle an
        # explicit choice before the model can reinterpret it as a new search.
        if isinstance(request.context, (DeleteCandidateContext, UpdateCandidateContext)):
            is_delete = isinstance(request.context, DeleteCandidateContext)
            selected = explicit_selection_numbers(
                request.text, {candidate.number for candidate in request.context.candidates},
                "on_delete" if is_delete else "on_update",
            )
            if selected is not None:
                return self._response(request, ModelDecision.model_validate({
                    "status": "ready", "intent": "on_delete" if is_delete else "on_update",
                    "message": "Удаляю выбранные записи." if is_delete else "Выбранные записи изменены.",
                    "action": {
                        "phase": "delete_confirmed" if is_delete else "update_confirmed",
                        "selected_numbers": selected,
                    },
                    "draft": None, "topic_changed": False,
                }), "candidate_selection")
        messages = build_messages(request, ModelDecision.model_json_schema())
        status_update_recovery = recover_task_status_update(request.text)
        for attempt in range(2):
            try:
                content = await self.gateway.complete(messages)
                decision = ModelDecision.model_validate(self._normalized_model_content(content))
                decision = normalize_task_deadline(request, decision)
                if status_update_recovery is not None:
                    expected = getattr(getattr(status_update_recovery.action, "changes", None), "status", None)
                    changes = getattr(decision.action, "changes", None) or getattr(decision.draft, "changes", None)
                    actual = getattr(changes, "status", None)
                    if decision.intent != "on_update" or (expected is not None and actual != expected):
                        return self._response(request, status_update_recovery, "status_update_recovery")
                if decision.status == "unsupported":
                    raise ServiceError(
                        400,
                        "unsupported_intent",
                        "Unsupported request",
                        "Supported requests create, list, search, update, or delete calendar entries.",
                    )
                return self._response(request, decision, "model")
            except (ValidationError, InvalidModelOutput) as exc:
                if status_update_recovery is not None:
                    return self._response(request, status_update_recovery, "status_update_recovery")
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

    @staticmethod
    def _normalized_model_content(content: str):
        try:
            value = json.loads(content)
        except json.JSONDecodeError:
            return content
        if not isinstance(value, dict):
            return value
        if value.get("intent") in ("create_event", "list_events", "search_events"):
            for name in ("action", "draft"):
                if isinstance(value.get(name), dict):
                    value[name] = {key: item for key, item in value[name].items() if item is not None}
        return value
