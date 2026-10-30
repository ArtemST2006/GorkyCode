"""Request-local model messages and calendar anchors."""

import json
from datetime import timedelta
from functools import lru_cache
from importlib.resources import files
from zoneinfo import ZoneInfo

from app.core.errors import ServiceError
from app.llm.protocols import ChatMessage
from app.schemas.job import JobRequest
from app.schemas.messages import MAX_RECENT_MESSAGES


@lru_cache(maxsize=1)
def load_system_prompt() -> str:
    return files("app.llm").joinpath("prompts/job.txt").read_text(encoding="utf-8")


def build_messages(request: JobRequest, result_schema: dict) -> list[ChatMessage]:
    try:
        local_time = request.request_time.astimezone(ZoneInfo(request.timezone))
    except (OverflowError, ValueError):
        raise ServiceError(
            400,
            "invalid_request",
            "Invalid request",
            "The request time cannot be represented in the specified timezone.",
        ) from None
    today = local_time.date()

    def shifted(days: int) -> str | None:
        try:
            return (today + timedelta(days=days)).isoformat()
        except OverflowError:
            return None

    payload = {
        "user_text": request.text,
        "local_request_time": local_time.isoformat(),
        "timezone": request.timezone,
        "local_date": today.isoformat(),
        "calendar_anchors": {
            "yesterday": shifted(-1),
            "tomorrow": shifted(1),
            "day_after_tomorrow": shifted(2),
            "this_week_start": shifted(-today.weekday()),
            "this_week_end": shifted(6 - today.weekday()),
            "this_week_end_exclusive": shifted(7 - today.weekday()),
            "next_week_start": shifted(7 - today.weekday()),
            "next_week_end": shifted(13 - today.weekday()),
            "next_week_end_exclusive": shifted(14 - today.weekday()),
        },
        "context": request.context.model_dump(mode="json", exclude_unset=True),
    }
    if "recent_messages" in payload["context"]:
        payload["context"]["recent_messages"] = payload["context"]["recent_messages"][-MAX_RECENT_MESSAGES:]
    return [
        {
            "role": "system",
            "content": load_system_prompt()
            + "\nJSON schema результата:\n"
            + json.dumps(result_schema, ensure_ascii=False),
        },
        {"role": "user", "content": json.dumps(payload, ensure_ascii=False)},
    ]
