"""Recover an unambiguous task status change when the model breaks its JSON contract."""

import re

from app.llm.schemas import ModelDecision


_CHANGE_VERB = re.compile(
    r"\b(?:переведи|переведите|перевести|поставь|поставьте|поставить|"
    r"отметь|отметьте|отметить|пометь|пометить|"
    r"измени|измените|изменить|поменяй|поменяйте|поменять|"
    r"обнови|обновите|обновить|установи|установите|установить|"
    r"беру|берем|возьми|возьмите|возьму|начни|начните|начинаю|"
    r"ставлю|перевожу)\b"
)
_TASK = re.compile(r"\bзадач(?:а|у|и|е|ей|ами|ах)\b")
_IN_PROGRESS = re.compile(r"\b(?:в\s+работу|в\s+работе|в\s+выполнение|на\s+выполнение|в\s+процессе)\b")
_COMPLETED = re.compile(r"\b(?:выполнено|выполненную|выполненные|завершено|завершенную|завершенные)\b")
_ACTIVE = re.compile(r"\b(?:в\s+активные|в\s+активную|активной)\b")


def recover_task_status_update(text: str) -> ModelDecision | None:
    """Recognize intent/status only; candidate selection still belongs to the user."""
    normalized = text.casefold().replace("ё", "е")
    verb = _CHANGE_VERB.search(normalized)
    if verb is None or _TASK.search(normalized) is None or "?" in normalized:
        return None
    if re.search(r"\bне\s+$", normalized[:verb.start()]):
        return None

    targets = [
        status for pattern, status in (
            (_IN_PROGRESS, "in_progress"),
            (_COMPLETED, "completed"),
            (_ACTIVE, "active"),
        ) if pattern.search(normalized)
    ]
    if len(targets) > 1:
        return None
    if not targets:
        if "статус" not in normalized:
            return None
        return ModelDecision.model_validate({
            "status": "needs_clarification", "intent": "on_update",
            "message": "На какой статус перевести задачу?",
            "action": None, "draft": {"record_types": ["task"]},
            "topic_changed": True,
        })

    return ModelDecision.model_validate({
        "status": "ready", "intent": "on_update",
        "message": "Подбираю задачи для смены статуса.",
        "action": {
            "phase": "find_candidates", "title_query": None,
            "date_from": None, "date_to_exclusive": None,
            "time_from": None, "time_to_exclusive": None,
            "record_types": ["task"], "changes": {"status": targets[0]},
        },
        "draft": None, "topic_changed": True,
    })
