"""Keep date-only tasks complete without inventing an appointment time."""

import calendar
import re
from datetime import timedelta
from zoneinfo import ZoneInfo

from app.llm.schemas import ModelDecision
from app.schemas.create_event import CreateEventDraft, TaskAction
from app.schemas.job import JobRequest


def normalize_task_deadline(request: JobRequest, decision: ModelDecision) -> ModelDecision:
    if decision.intent != "create_event" or decision.status == "unsupported":
        return decision

    action = decision.action if isinstance(decision.action, TaskAction) else None
    draft = decision.draft if isinstance(decision.draft, CreateEventDraft) else None
    if action is None and (draft is None or draft.record_type != "task"):
        return decision

    current = action or draft
    if current is None or not current.title:
        return decision

    date = current.date
    time = current.time
    text = request.text.casefold().replace("ё", "е")
    local_date = request.request_time.astimezone(ZoneInfo(request.timezone)).date()

    if re.search(r"до конца (?:этой |текущей )?недели\b", text):
        date = (local_date + timedelta(days=6 - local_date.weekday())).isoformat()
    elif re.search(r"до конца следующей недели\b", text):
        date = (local_date + timedelta(days=13 - local_date.weekday())).isoformat()
    elif re.search(r"до конца (?:этого |текущего )?месяца\b", text):
        last_day = calendar.monthrange(local_date.year, local_date.month)[1]
        date = local_date.replace(day=last_day).isoformat()

    if date is None:
        return decision

    # A deadline has a calendar date, not an implied 23:59. Preserve a time
    # explicitly named by the user or already collected in the dialogue.
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

    if action is not None and date == action.date and time == action.time:
        return decision

    values = decision.model_dump(mode="json", exclude_unset=True)
    values["status"] = "ready"
    values["action"] = {"record_type": "task", "title": current.title, "date": date}
    if time is not None:
        values["action"]["time"] = time
    values["draft"] = None
    values["message"] = f"Задача «{current.title}» создана."
    return ModelDecision.model_validate(values)
