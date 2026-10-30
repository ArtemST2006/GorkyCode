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


def _parse_relative_offset_minutes(text: str) -> float | None:
    """Return the offset a user's text implies, in minutes."""
    normalized = text.casefold().replace("ё", "е")
    if re.search(r"\bчерез\s+полчаса\b", normalized):
        return 30.0
    if re.search(r"\bчерез\s+полтора\s+часа\b", normalized):
        return 90.0
    if re.search(r"\bчерез\s+час(?:\s+с\s+половиной)?\b", normalized):
        return 90.0 if "с половиной" in normalized else 60.0
    match = re.search(
        r"\bчерез\s+(\d+)\s+с\s+половиной\s+(минут(?:у|ы)?|час(?:а|ов)?)\b",
        normalized,
    )
    if match:
        value = float(match.group(1)) + 0.5
        multiplier = 60 if match.group(2).startswith("час") else 1
        return value * multiplier if value * multiplier <= 525600 else None
    match = re.search(
        r"\bчерез\s+(\d+(?:[.,]\d+)?)\s*(минут(?:у|ы)?|мин|час(?:а|ов)?|ч)\b",
        normalized,
    )
    if match:
        value = float(match.group(1).replace(",", "."))
        multiplier = 60 if match.group(2).startswith(("час", "ч")) else 1
        minutes = value * multiplier
        return minutes if 0 < minutes <= 525600 else None
    return None


def _relative_due(request: JobRequest, minutes: float) -> datetime:
    """Add elapsed time and round up to a representable local minute."""
    due = request.request_time.astimezone(timezone.utc) + timedelta(minutes=minutes)
    if due.second or due.microsecond:
        due = due.replace(second=0, microsecond=0) + timedelta(minutes=1)
    return due.astimezone(ZoneInfo(request.timezone))


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
    due_at = _relative_due(request, relative_offset) if relative_offset is not None else None
    if due_at is not None:
        date = due_at.date().isoformat()
        time = due_at.strftime("%H:%M")
        if record_type is None and re.search(r"\bнапомни\b", text):
            record_type = "reminder"

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
            r"\b(?:в|к)\s*(?:[01]?\d|2[0-3])(?:\s*ч(?:ас(?:а|ов)?)?\.?)?\b"
            r"(?!\s*(?:[./-]\d|-(?:го|е|му|м)\b|"
            r"(?:января|февраля|марта|апреля|мая|июня|июля|августа|"
            r"сентября|октября|ноября|декабря)\b))|"
            r"\b(?:в|к)\s+(?:одному|двум|трем|четырем|"
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
    values = decision.model_dump(mode="json", exclude_unset=True)
    values["status"] = "ready"
    values["action"] = {"record_type": current_record_type, "title": current.title, "date": date}
    if current_record_type != "task":
        values["action"]["time"] = time
    elif time is not None:
        values["action"]["time"] = time
    values["draft"] = None
    if due_at is not None:
        time_label = due_at.strftime("%H:%M")
        values["message"] = (
            f"Напоминание «{current.title}» установлено на {date} в {time_label}."
            if current_record_type == "reminder"
            else f"Запись «{current.title}» создана на {date} в {time_label}."
        )
    else:
        values["message"] = f"Задача «{current.title}» создана."
    return ModelDecision.model_validate(values)
