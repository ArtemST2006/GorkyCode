"""Construct public dialogue history and responses in application code."""

import re

from app.core.errors import InvalidModelOutput
from app.llm.schemas import DeleteSelectionAction, ModelDecision, UpdateSelectionAction
from app.schemas.context import StoredContext
from app.schemas.delete_event import (
    DeleteCandidateContext,
    DeleteConfirmedAction,
    DeleteFindCandidatesAction,
)
from app.schemas.job import JOB_RESPONSE_ADAPTER, JobRequest, JobResponse
from app.schemas.messages import MAX_RECENT_MESSAGES
from app.schemas.update_event import (
    UpdateCandidateContext,
    UpdateConfirmedAction,
    UpdateFindCandidatesAction,
)
from app.services.plain_message import plain_message


_DELETE_FILTERS = (
    "title_query", "date_from", "date_to_exclusive", "time_from", "time_to_exclusive",
    "record_types",
)

_SELECTION_NUMBERS = {
    "первый": 1, "первую": 1, "первого": 1, "первой": 1, "первое": 1, "один": 1, "одну": 1,
    "второй": 2, "вторую": 2, "второго": 2, "второе": 2, "два": 2, "две": 2,
    "третий": 3, "третью": 3, "третьего": 3, "третьей": 3, "три": 3,
    "четвертый": 4, "четвертую": 4, "четвертого": 4, "четыре": 4,
    "пятый": 5, "пятую": 5, "пятого": 5, "пять": 5,
    "шестой": 6, "шестую": 6, "шестого": 6, "шесть": 6,
    "седьмой": 7, "седьмую": 7, "седьмого": 7, "семь": 7,
    "восьмой": 8, "восьмую": 8, "восьмого": 8, "восемь": 8,
    "девятый": 9, "девятую": 9, "девятого": 9, "девять": 9,
    "десятый": 10, "десятую": 10, "десятого": 10, "десять": 10,
}
_SELECTION_WORDS = {
    "выбери", "выбрать",
    "выбираю", "да", "подтверждаю", "пожалуйста", "запись", "записи",
    "задачу", "задачи", "встречу", "встречи", "напоминание", "напоминания",
    "событие", "события", "номер", "номера", "пункт", "пункты", "и",
    "из", "списка", "показанные", "показанных", "найденных", "кандидатов", "все", "всех",
}
_OPERATION_WORDS = {
    "on_delete": {"удали", "удалить", "удаляй", "убери", "убрать"},
    "on_update": {"измени", "изменить", "обнови", "обновить", "примени", "применить"},
}


def explicit_selection_numbers(text: str, shown: set[int], intent: str = "on_delete") -> list[int] | None:
    """Recognize an unambiguous selection from the displayed numbered list."""
    normalized = text.casefold().replace("ё", "е")
    # Do not discard question marks, signs or date/time separators: doing so
    # can turn a question or a timestamp into a destructive confirmation.
    if (not shown or not re.fullmatch(r"[а-яa-z0-9\s№,.!]+", normalized)
            or re.search(r"\d[.,]\d", normalized)):
        return None
    tokens = re.findall(r"№\s*\d+|\d+|[а-яa-z]+", normalized)
    allowed = _SELECTION_WORDS | _OPERATION_WORDS.get(intent, set())
    if not tokens:
        return None
    selected = []
    all_requested = False
    for token in tokens:
        if token in ("все", "всех"):
            all_requested = True
        elif token.startswith("№") or token.isdigit():
            selected.append(int(token.lstrip("№ ")))
        elif token in _SELECTION_NUMBERS:
            selected.append(_SELECTION_NUMBERS[token])
        elif token not in allowed:
            return None
    if all_requested:
        return sorted(shown) if not selected else None
    if not selected or len(selected) != len(set(selected)) or not set(selected) <= shown:
        return None
    return selected


def _repeat_candidate_search(request: JobRequest, decision: ModelDecision) -> dict:
    """An unclear choice must show candidates again: clarification drops them from context."""
    current = request.context
    draft = (
        decision.draft.model_dump(mode="json", exclude_unset=True)
        if decision.draft is not None else {}
    )
    if not any(draft.get(name) is not None for name in _DELETE_FILTERS):
        draft = current.draft.model_dump(mode="json", exclude_unset=True)
    try:
        return DeleteFindCandidatesAction.model_validate(
            {"phase": "find_candidates", **{name: draft.get(name) for name in _DELETE_FILTERS}}
        ).model_dump(mode="json")
    except ValueError as exc:
        raise InvalidModelOutput("Delete clarification lost the candidate search") from exc


def _explicit_selection(text: str, numbers: list[int], shown: set[int], intent: str) -> bool:
    """Never let a model turn an ambiguous acknowledgement into a DELETE."""
    selected = explicit_selection_numbers(text, shown, intent)
    return selected is not None and set(selected) == set(numbers)


def _repeat_update_search(request: JobRequest, decision: ModelDecision) -> dict:
    current = request.context
    draft = current.draft.model_dump(mode="json", exclude_unset=True)
    if decision.draft is not None:
        supplied = decision.draft.model_dump(mode="json", exclude_unset=True)
        if supplied.get("changes"):
            draft["changes"] = {**draft.get("changes", {}), **supplied.pop("changes")}
        draft.update({key: value for key, value in supplied.items() if value is not None})
    try:
        return UpdateFindCandidatesAction.model_validate(
            {"phase": "find_candidates", **{name: draft.get(name) for name in _DELETE_FILTERS},
             "changes": draft.get("changes")}
        ).model_dump(mode="json", exclude_unset=True)
    except ValueError as exc:
        raise InvalidModelOutput("Update clarification lost search or changes") from exc


def build_job_response(request: JobRequest, decision: ModelDecision) -> JobResponse:
    # Empty/date-only drafts match several internal union branches. Re-validate
    # the JSON shape against the selected intent, not the branch Python chose.
    values = decision.model_dump(mode="json", exclude_unset=True)
    status = decision.status
    message = decision.message
    if decision.intent in ("create_event", "list_events", "search_events"):
        # Optional model fields may be emitted as explicit null. Public actions
        # use omitted fields for absent filters; normalize before validation.
        if isinstance(values.get("action"), dict):
            values["action"] = {key: value for key, value in values["action"].items() if value is not None}
        if isinstance(values.get("draft"), dict):
            values["draft"] = {key: value for key, value in values["draft"].items() if value is not None}
    if (
        decision.intent == "on_delete"
        and status == "needs_clarification"
        and isinstance(request.context, DeleteCandidateContext)
        and request.context.candidates
        and not decision.topic_changed
    ):
        values["action"] = _repeat_candidate_search(request, decision)
        values["draft"] = None
        status = "ready"
        message = "Подбираю записи для удаления."
    if decision.intent == "on_update" and status == "needs_clarification":
        action = None
        if isinstance(request.context, UpdateCandidateContext) and not decision.topic_changed:
            action = _repeat_update_search(request, decision)
        else:
            # Updates are patches, not new events. A valid filter and one change
            # are enough to FIND candidates, even if the model asks for fields
            # that should stay unchanged. Never infer values or confirm a write.
            draft = values["draft"] or {}
            try:
                action = UpdateFindCandidatesAction.model_validate({
                    "phase": "find_candidates",
                    **{name: draft.get(name) for name in _DELETE_FILTERS},
                    "changes": draft.get("changes"),
                }).model_dump(mode="json", exclude_unset=True)
            except ValueError:
                pass  # Missing/invalid search or changes still need clarification.
        if action is not None:
            values["action"] = action
            values["draft"] = None
            status = "ready"
            message = "Подбираю записи для изменения."
    message = plain_message(message)
    history = []
    if isinstance(request.context, (StoredContext, DeleteCandidateContext, UpdateCandidateContext)) and not decision.topic_changed:
        history = [message.model_dump() for message in request.context.recent_messages]
    history.append({"role": "user", "text": request.text})
    if decision.intent == "chat":
        history.append({"role": "assistant", "text": message})
    if status == "needs_clarification":
        history.append({"role": "assistant", "text": message})
    if decision.intent == "on_delete":
        if isinstance(decision.action, DeleteSelectionAction):
            if decision.topic_changed or not isinstance(request.context, DeleteCandidateContext):
                raise InvalidModelOutput("Delete confirmation needs saved candidates")
            candidates = {candidate.number: candidate.id for candidate in request.context.candidates}
            if any(number not in candidates for number in decision.action.selected_numbers):
                raise InvalidModelOutput("Delete selection contains an unknown candidate number")
            if _explicit_selection(request.text, decision.action.selected_numbers, set(candidates), "on_delete"):
                values["action"] = DeleteConfirmedAction(
                    phase="delete_confirmed",
                    task_ids=[candidates[number] for number in decision.action.selected_numbers],
                ).model_dump(mode="json")
            else:
                values["action"] = _repeat_candidate_search(request, decision)
                message = "Выберите номера записей для удаления."
    if decision.intent == "on_update" and isinstance(decision.action, UpdateSelectionAction):
        if decision.topic_changed or not isinstance(request.context, UpdateCandidateContext):
            raise InvalidModelOutput("Update confirmation needs saved candidates")
        candidates = {candidate.number: candidate.id for candidate in request.context.candidates}
        if any(number not in candidates for number in decision.action.selected_numbers):
            raise InvalidModelOutput("Update selection contains an unknown candidate number")
        if _explicit_selection(request.text, decision.action.selected_numbers, set(candidates), "on_update"):
            values["action"] = UpdateConfirmedAction(
                phase="update_confirmed",
                record_ids=[candidates[number] for number in decision.action.selected_numbers],
            ).model_dump(mode="json")
        else:
            values["action"] = _repeat_update_search(request, decision)
            message = "Выберите номера записей для изменения."
    # No implicit merging: a new action/draft is the full current topic state.
    response = {
        "user_id": request.user_id,
        "task_id": request.task_id,
        "status": status,
        "message": message,
        "action": values["action"],
        "context": {"recent_messages": history[-MAX_RECENT_MESSAGES:], "draft": values["draft"]},
    }
    if decision.intent not in ("on_delete", "on_update"):
        response["intent"] = decision.intent
    return JOB_RESPONSE_ADAPTER.validate_python(response)
