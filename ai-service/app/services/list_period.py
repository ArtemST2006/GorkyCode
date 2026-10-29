"""Resolve explicit period replies to a pending list question without an LLM."""

import re
from datetime import date, timedelta
from zoneinfo import ZoneInfo

from app.llm.schemas import ModelDecision
from app.schemas.context import EmptyContext, StoredContext
from app.schemas.job import JobRequest


def _period(text: str, today: date) -> tuple[date, date] | None:
    # Full-string matching is intentional: a new command, negation or extra
    # condition must still reach the model. Never extract a date from prose here.
    text = " ".join(text.casefold().replace("ё", "е").split()).strip(" .!")
    text = re.sub(r",? пожалуйста$", "", text).strip()
    monday = today - timedelta(days=today.weekday())
    if text in {"сегодня", "на сегодня", "за сегодня"}:
        return today, today + timedelta(days=1)
    if text in {"завтра", "на завтра", "за завтра"}:
        return today + timedelta(days=1), today + timedelta(days=2)
    if text in {"послезавтра", "на послезавтра", "за послезавтра"}:
        return today + timedelta(days=2), today + timedelta(days=3)
    if text in {"до конца недели", "до конца этой недели", "до конца текущей недели"}:
        return today, monday + timedelta(days=7)
    if text in {"до конца следующей недели"}:
        return today, monday + timedelta(days=14)
    if text in {"на этой неделе", "за эту неделю", "эта неделя", "на текущей неделе"}:
        return monday, monday + timedelta(days=7)
    if text in {"на следующей неделе", "за следующую неделю", "следующая неделя"}:
        return monday + timedelta(days=7), monday + timedelta(days=14)
    if text in {"до конца месяца", "до конца этого месяца", "до конца текущего месяца",
                "в этом месяце", "за этот месяц", "этот месяц"}:
        first = today.replace(day=1)
        next_month = (first + timedelta(days=32)).replace(day=1)
        return (today if text.startswith("до конца") else first), next_month
    if re.fullmatch(r"(?:на )?\d{4}-\d{2}-\d{2}", text):
        day = date.fromisoformat(text.removeprefix("на "))
        return day, day + timedelta(days=1)
    return None


def resolve_list_period_reply(request: JobRequest) -> ModelDecision | None:
    """Only fill a period when the preceding turn explicitly asked to list one.

    Context union types overlap (an empty list draft can parse as a create
    draft), so inspect its actual fields and the last assistant question.
    Missing context, search topics, mutations and partial intervals are left
    to the model. We never guess which conversation an orphan reply belongs to.
    """
    context = request.context
    if not isinstance(context, StoredContext):
        return None
    draft = context.draft.model_dump(mode="json", exclude_unset=True)
    if set(draft) - {"date_from", "date_to_exclusive", "record_types"}:
        return None
    if draft.get("date_from") is not None or draft.get("date_to_exclusive") is not None:
        return None
    last = context.recent_messages[-1]
    question = last.text.casefold().replace("ё", "е")
    if last.role != "assistant" or not re.search(r"период|диапазон дат", question):
        return None
    if not re.search(r"показ|вывест|отобраз", question):
        return None
    # A proposal to show something is not necessarily a request for a period.
    if not re.search(r"какой|какие|уточни|укажите|укажи", question):
        return None
    previous_user = next(
        (message.text.casefold().replace("ё", "е")
         for message in reversed(context.recent_messages) if message.role == "user"), ""
    )
    if not re.search(r"\b(?:покажи|покажите|показать|выведи|вывести|список|что у меня|какие)\b",
                     previous_user):
        return None
    if re.search(r"\b(?:удал\w*|убер\w*|измен\w*|обнов\w*|перенес\w*|создай|создать|назнач\w*)",
                 previous_user + " " + question):
        return None
    if "кандидат" in question:
        return None
    # A legacy draft might have lost a title/status filter. Do not turn a
    # subject-specific search into a broad list just because its query is absent.
    if re.search(r"\b(?:с|со|про|по|о|об|без|кроме|только|найди|актив\w*|заверш\w*)\b",
                 previous_user + " " + question):
        return None
    try:
        today = request.request_time.astimezone(ZoneInfo(request.timezone)).date()
        period = _period(request.text, today)
    except (ValueError, OverflowError):
        return None
    if period is None:
        return None
    record_types = draft.get("record_types")
    if record_types is None:
        # Older prompts returned an empty draft even when the question itself
        # retained the type. Recover only explicit types in that question.
        record_types = [
            kind for pattern, kind in (
                (r"\bвстреч\w*", "meeting"),
                (r"\bзадач\w*", "task"),
                (r"\bнапоминан\w*", "reminder"),
            ) if re.search(pattern, question)
        ]
        if not record_types:
            # An empty legacy draft plus a generic question may have lost a
            # filter from the original request; let the model recover it.
            return None
    action = {"date_from": period[0].isoformat(), "date_to_exclusive": period[1].isoformat()}
    action["record_types"] = record_types
    return ModelDecision.model_validate({
        "status": "ready", "intent": "list_events", "topic_changed": False,
        "message": "Проверяю события за указанный период.", "action": action, "draft": None,
    })


def is_orphan_period_reply(request: JobRequest) -> bool:
    """A bare known period without dialogue state cannot identify an operation."""
    if not isinstance(request.context, EmptyContext):
        return False
    try:
        today = request.request_time.astimezone(ZoneInfo(request.timezone)).date()
        return _period(request.text, today) is not None
    except (ValueError, OverflowError):
        return False
