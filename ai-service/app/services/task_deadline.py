"""Resolve date-only tasks and relative-time reminders without inventing details."""

import calendar
import re
from datetime import datetime, timedelta, timezone
from zoneinfo import ZoneInfo

from app.llm.schemas import ModelDecision
from app.schemas.create_event import (
    CreateEventDraft,
    TaskAction,
    TimedEventAction,
)
from app.schemas.job import JobRequest


_RELATIVE_MINUTE_UNIT = re.compile(
    r"\bчерез\s+(?:(\d+(?:[.,]\d+)?)\s*(?:минут(?:у|ы)?|мин)\b"
    r"|пол(?:тора)?\s*минут)",
    re.IGNORECASE,
)


def _parse_relative_offset_minutes(text: str) -> float | None:
    """Return the offset a user's text implies, in minutes."""
    normalized = text.casefold().replace("ё", "е")
    if re.search(r"\bчерез\s+пол(?:тора)?\s*минут\b", normalized):
        return 90.0 if "полтора" in normalized else 30.0
    if re.search(r"\bчерез\s+час(?:\s+с\s+половиной)?\b", normalized):
        return 90.0 if "с половиной" in normalized else 60.0
    match = re.search(r"\bчерез\s+(\d+(?:[.,]\d+)?)\s*(?:минут(?:у|ы)?|мин)\b", normalized)
    if match:
        value = float(match.group(1).replace(",", "."))
        return value
    # "5 с половиной минут(ы/е)" or "5.5 мин"
    match = re.search(r"\bчерез\s+(\d+)\s+с\s+половиной\s+минут(?:у|ы)?\b", normalized)
    if match:
        return float(match.group(1)) + 0.5
    return None


def _local_now(request: JobRequest) -> datetime:
    tz = ZoneInfo(request.timezone)
    return request.request_time.astimezone(tz)


def _ceil_minutes(value: datetime) -> str:
    minute_overflow, minute = divmod(value.minute, 60)
    # ceil seconds, trailing seconds do not fit HH:MM, so round upward
    if value.second or value.microsecond:
        minute += 1
        if minute >= 60:
            minute -= 60
            minute_overflow += 1
    hour = (value.hour + minute_overflow) % 24
    return f"{hour:02d}:{minute:02d}"


def normalize_task_deadline(request: JobRequest, decision: ModelDecision) -> ModelDecision:
    if decision.intent != "create_event" or decision.status == "unsupported":
        return decision

    action = None
    if isinstance(decision.action, (TaskAction, TimedEventAction)):
        action = decision.action
    draft = decision.draft if isinstance(decision.draft, CreateEventDraft) else None
    if action is None and (draft is None or not getattr(draft, "title", None)):
        return decision

    current = action or draft
    if current is None or not getattr(current, "title", None):
        return decision

    text = request.text.casefold().replace("ё", "е")
    local_date = request.request_time.astimezone(ZoneInfo(request.timezone)).date()
    date = getattr(current, "date", None)
    time = getattr(current, "time", None)
    record_type = getattr(current, "record_type", None)

    if re.search(r"до конца (?:этой |текущей )?недели\b", text):
        date = (local_date + timedelta(days=6 - local_date.weekday())).isoformat()
    elif re.search(r"до конца следующей недели\b", text):
        date = (local_date + timedelta(days=13 - local_date.weekday())).isoformat()
    elif re.search(r"до конца (?:этого |текущего )?месяца\b", text):
        last_day = calendar.monthrange(local_date.year, local_date.month)[1]
        date = local_date.replace(day=last_day).isoformat()

    # New behavior: phrase "через N ... минут" (including halves) produces
    # a complete reminder/meeting time instead of asking for clarification.
    relative_offset = _parse_relative_offset_minutes(text)
    if relative_offset is not None:
        due_at = _local_now(request) + timedelta(minutes=relative_offset)
        date = due_at.date().isoformat()
        time = _ceil_minutes(due_at)
        if record_type is None:
            record_type = "reminder" if "напомни" in text else "meeting"

    if date is None:
        return decision
    if record_type in {"meeting", "reminder"} and not time:
        return decision

    current_record_type = record_type or getattr(current, "record_type", None)
    if current_record_type not in {"task", "meeting", "reminder"}:
        return decision

    deadline = "до конца" in text
    explicit_time = bool(
        re.search(
            r"\b\d{1,2}:\d{2}\b|\b\d{1,2}\s*час(?:а|ов)?\b|"
            r"\b(?:в|к)\s+(?:\d{1,2}\b|одному|двум|трем|четырем|"
            r"пяти|шести|семи|восьми|девяти|десяти)|"
            r"\b(?:утром|днем|вечером|ночью|полдень|полночь|"
            r"к вечеру|на утро)\b",
            text,
        )
    )
    context_draft = getattr(request.context, "draft", None)
    if deadline and not explicit_time and getattr(context_draft, "time", None) is None:
        time = None
    if current_record_type in {"meeting", "reminder"} and time is None:
        return decision

    if action is not None and getattr(action, "record_type", None) == current_record_type and date == getattr(action, "date", None) and time == getattr(action, "time", None) and decision.status == "ready":
        return decision
    if decision.status == "ready":
        # Avoid changing an already complete action unless a relative time
        # forced a recalculation.
        if relative_offset is None:
            return decision

    values = decision.model_dump(mode="json", exclude_unset=True)
    values["status"] = "ready"
    values["action"] = {"record_type": current_record_type, "title": current.title, "date": date}
    if current_record_type != "task":
        values["action"]["time"] = time
    elif time is not None:
        values["action"]["time"] = time
    values["draft"] = None
    if relative_offset is not None:
        due = _local_now(request) + timedelta(minutes=relative_offset)
        time_label = due.strftime("%H:%M")
        values["message"] = (
            f"Напоминание «{current.title}» установлено на {date} в {time_label}."
            if current_record_type == "reminder"
            else f"Запись create «{current.title}» создана на {date} в {time_label}."
        )
    else:
        values["message"] = f"Задача «{current.title}» создана."
    return ModelDecision.model_validate(values)
