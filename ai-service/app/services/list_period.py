"""Reject context-dependent fragments before a model can guess an action."""

from datetime import date
import re

from app.schemas.context import EmptyContext
from app.schemas.job import JobRequest


_BARE_PERIODS = {
    "сегодня", "на сегодня", "за сегодня", "завтра", "на завтра", "за завтра",
    "послезавтра", "на послезавтра", "за послезавтра",
    "до конца недели", "до конца этой недели", "до конца текущей недели",
    "до конца следующей недели", "на этой неделе", "за эту неделю", "эта неделя",
    "на текущей неделе", "на следующей неделе", "за следующую неделю",
    "следующая неделя", "до конца месяца", "до конца этого месяца",
    "до конца текущего месяца", "в этом месяце", "за этот месяц", "этот месяц",
}


def is_orphan_reply(request: JobRequest) -> bool:
    """A bare answer without context cannot safely select a calendar action."""
    if not isinstance(request.context, EmptyContext):
        return False
    text = " ".join(request.text.casefold().replace("ё", "е").split()).strip(" .!")
    text = re.sub(r",? пожалуйста$", "", text).strip()
    if text in _BARE_PERIODS:
        return True
    if text in {"да", "нет", "первый", "второй", "третий", "все показанные"}:
        return True
    if re.fullmatch(r"(?:№\s*)?\d{1,2}", text):
        return True
    if re.fullmatch(r"(?:в|к|на)\s+\d{1,2}(?::\d{2})?(?:\s*час(?:а|ов)?)?", text):
        return True
    if re.fullmatch(r"\d{1,2}:\d{2}", text):
        return True
    if re.fullmatch(r"(?:на\s+)?\d{1,2}\s+[а-я]+(?:\s+\d{4})?", text):
        return True
    if re.fullmatch(r"(?:на )?\d{4}-\d{2}-\d{2}", text):
        try:
            date.fromisoformat(text.removeprefix("на "))
        except ValueError:
            return False
        return True
    return False
