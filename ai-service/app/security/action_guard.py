"""Check final proposals with a separately injected model gateway."""

import asyncio
import json
import math
from datetime import timedelta
from functools import lru_cache
from importlib.resources import files
from zoneinfo import ZoneInfo

from pydantic import ValidationError

from app.core.errors import InvalidModelOutput, ServiceError
from app.llm.protocols import ChatMessage, ModelGateway
from app.schemas.delete_event import DeleteCandidateContext
from app.schemas.job import JobRequest, JobResponse
from app.schemas.update_event import UpdateCandidateContext
from app.security.guard_schemas import GuardVerdict
from app.services.context import explicit_selection_numbers


_INTENTS = {"create_event", "list_events", "search_events", "on_delete", "on_update"}
_ACTION_FIELDS = {
    "create_event": {"record_type", "title", "date", "time"},
    "list_events": {"date_from", "date_to_exclusive", "record_types"},
    "search_events": {"query", "date_from", "date_to_exclusive", "record_types"},
    "on_delete": {
        "phase", "title_query", "date_from", "date_to_exclusive",
        "time_from", "time_to_exclusive", "record_types",
    },
    "on_update": {
        "phase", "title_query", "date_from", "date_to_exclusive",
        "time_from", "time_to_exclusive", "record_types", "changes",
    },
}
_DRAFT_FIELDS = {
    "record_type", "title", "date", "time", "query", "title_query",
    "date_from", "date_to_exclusive", "time_from", "time_to_exclusive",
    "record_types", "changes",
}
_CHANGE_FIELDS = {"title", "date", "time", "status"}
MAX_GUARD_PAYLOAD_CHARS = 48_000
MAX_GUARD_RESPONSE_CHARS = 2_048
MAX_GUARD_USER_HISTORY = 6


class GuardError(ServiceError):
    """Safe guard failure; never a verdict authorizing an action."""

    def __init__(self, code: str, status_code: int = 503) -> None:
        super().__init__(
            status_code, code, "Action verification unavailable",
            "The proposed action could not be verified safely.",
        )


@lru_cache(maxsize=1)
def _load_prompt() -> str:
    return files("app.security").joinpath("prompts/action_guard.txt").read_text(encoding="utf-8")


def _unique_object(pairs: list[tuple[str, object]]) -> dict:
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate verdict field")
        result[key] = value
    return result


def _changes(value: dict) -> dict:
    return {key: item for key, item in value.items() if key in _CHANGE_FIELDS}


def _confirmed_action(request: JobRequest, intent: str, action: dict) -> dict:
    expected_context = DeleteCandidateContext if intent == "on_delete" else UpdateCandidateContext
    expected_phase = "delete_confirmed" if intent == "on_delete" else "update_confirmed"
    id_field = "task_ids" if intent == "on_delete" else "record_ids"
    if not isinstance(request.context, expected_context) or action.get("phase") != expected_phase:
        raise GuardError("guard_invalid_proposal", 500)
    by_id = {candidate.id: candidate.number for candidate in request.context.candidates}
    selected_ids = action.get(id_field, [])
    if not selected_ids or any(record_id not in by_id for record_id in selected_ids):
        raise GuardError("guard_invalid_proposal", 500)
    selected = [by_id[record_id] for record_id in selected_ids]
    explicitly_selected = explicit_selection_numbers(request.text, set(by_id.values()), intent)
    if explicitly_selected is None or set(selected) != set(explicitly_selected):
        raise GuardError("guard_invalid_proposal", 500)
    result = {"phase": expected_phase, "selected_numbers": selected}
    if intent == "on_update":
        saved = request.context.draft.changes
        if saved is None:
            raise GuardError("guard_invalid_proposal", 500)
        saved_changes = _changes(saved.model_dump(mode="json", exclude_unset=True, exclude_none=True))
        if not saved_changes:
            raise GuardError("guard_invalid_proposal", 500)
        result["saved_changes"] = saved_changes
    return result


def _build_payload(request: JobRequest, *, intent: str, proposal: JobResponse) -> dict:
    if intent not in _INTENTS or getattr(proposal, "intent", intent) != intent:
        raise GuardError("guard_invalid_proposal", 500)
    action = proposal.action.model_dump(mode="json", exclude_unset=True) if proposal.action else None
    if action is not None:
        if action.get("phase") in {"delete_confirmed", "update_confirmed"}:
            if intent not in {"on_delete", "on_update"}:
                raise GuardError("guard_invalid_proposal", 500)
            action = _confirmed_action(request, intent, action)
        else:
            if set(action) - _ACTION_FIELDS[intent]:
                raise GuardError("guard_invalid_proposal", 500)
            if isinstance(action.get("changes"), dict):
                action["changes"] = _changes(action["changes"])
    # Assistant output is omitted: it can contain echoed database titles or the
    # first model's message. Draft and prior user turns preserve continuations.
    prior_users = [
        {"text": message.text}
        for message in getattr(request.context, "recent_messages", [])
        if message.role == "user"
    ][-MAX_GUARD_USER_HISTORY:]
    draft_model = getattr(request.context, "draft", None)
    draft = draft_model.model_dump(mode="json", exclude_unset=True, exclude_none=True) if draft_model else {}
    draft = {key: value for key, value in draft.items() if key in _DRAFT_FIELDS}
    if isinstance(draft.get("changes"), dict):
        draft["changes"] = _changes(draft["changes"])
    context = {"history": prior_users, "draft": draft}
    if isinstance(request.context, (DeleteCandidateContext, UpdateCandidateContext)):
        context["candidate_numbers"] = [candidate.number for candidate in request.context.candidates]
    try:
        local_time = request.request_time.astimezone(ZoneInfo(request.timezone))
        today = local_time.date()

        def shifted(days: int) -> str | None:
            try:
                return (today + timedelta(days=days)).isoformat()
            except OverflowError:
                return None

        anchors = {
            "yesterday": shifted(-1), "today": today.isoformat(), "tomorrow": shifted(1),
            "day_after_tomorrow": shifted(2),
            "this_week_start": shifted(-today.weekday()),
            "this_week_end": shifted(6 - today.weekday()),
            "next_week_start": shifted(7 - today.weekday()),
            "next_week_end": shifted(13 - today.weekday()),
            "next_week_end_exclusive": shifted(14 - today.weekday()),
        }
    except (OverflowError, ValueError):
        raise GuardError("guard_invalid_context", 400) from None
    return {
        "user_text": request.text, "context": context,
        "local_request_time": local_time.isoformat(), "timezone": request.timezone,
        "calendar_anchors": anchors,
        "proposed": {"intent": intent, "status": proposal.status, "action": action},
    }


class ActionGuard:
    def __init__(self, gateway: ModelGateway, *, timeout_seconds: float = 10.0) -> None:
        if not math.isfinite(timeout_seconds) or timeout_seconds <= 0:
            raise ValueError("Guard timeout must be finite and positive")
        self.gateway = gateway
        self.timeout_seconds = timeout_seconds

    async def verify(
        self, request: JobRequest, *, intent: str, proposal: JobResponse,
    ) -> GuardVerdict:
        payload = json.dumps(_build_payload(request, intent=intent, proposal=proposal), ensure_ascii=False)
        if len(payload) > MAX_GUARD_PAYLOAD_CHARS:
            # Truncating silently could remove a negation or an authorization.
            raise GuardError("guard_context_too_large", 400)
        messages: list[ChatMessage] = [
            {"role": "system", "content": _load_prompt() + "\nJSON schema:\n" + json.dumps(GuardVerdict.model_json_schema(), ensure_ascii=False)},
            {"role": "user", "content": payload},
        ]
        try:
            async with asyncio.timeout(self.timeout_seconds):
                content = await self.gateway.complete(messages)
                if not isinstance(content, str) or len(content) > MAX_GUARD_RESPONSE_CHARS:
                    raise GuardError("guard_invalid_output")
                # Reject ambiguous JSON rather than accepting the last value
                # of repeated decision/reason fields.
                parsed = json.loads(content, object_pairs_hook=_unique_object)
                return GuardVerdict.model_validate(parsed)
        except TimeoutError:
            raise GuardError("guard_timeout", 504) from None
        except (ValueError, ValidationError, InvalidModelOutput):
            raise GuardError("guard_invalid_output") from None
        except GuardError:
            raise
        except Exception:
            # Gateway errors/refusals and unexpected SDK errors never authorize
            # an action, and their potentially sensitive text is not forwarded.
            raise GuardError("guard_unavailable") from None
