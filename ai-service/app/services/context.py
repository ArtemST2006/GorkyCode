"""Construct public dialogue history and responses in application code."""

from app.core.errors import InvalidModelOutput
from app.llm.schemas import DeleteSelectionAction, ModelDecision
from app.schemas.context import StoredContext
from app.schemas.delete_event import (
    DeleteCandidateContext,
    DeleteConfirmedAction,
    DeleteFindCandidatesAction,
)
from app.schemas.job import JOB_RESPONSE_ADAPTER, JobRequest, JobResponse


_DELETE_FILTERS = (
    "title_query", "date_from", "date_to_exclusive", "time_from", "time_to_exclusive",
    "record_types",
)


def _repeat_candidate_search(request: JobRequest, decision: ModelDecision) -> dict:
    """An unclear choice must show candidates again: clarification drops them from context."""
    current = request.context
    draft = decision.draft.model_dump(mode="json", exclude_unset=True)
    if not any(draft.get(name) is not None for name in _DELETE_FILTERS):
        draft = current.draft.model_dump(mode="json", exclude_unset=True)
    try:
        return DeleteFindCandidatesAction.model_validate(
            {"phase": "find_candidates", **{name: draft.get(name) for name in _DELETE_FILTERS}}
        ).model_dump(mode="json")
    except ValueError as exc:
        raise InvalidModelOutput("Delete clarification lost the candidate search") from exc


def build_job_response(request: JobRequest, decision: ModelDecision) -> JobResponse:
    # Empty/date-only drafts match several internal union branches. Re-validate
    # the JSON shape against the selected intent, not the branch Python chose.
    values = decision.model_dump(mode="json", exclude_unset=True)
    status = decision.status
    message = decision.message
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
    history = []
    if isinstance(request.context, (StoredContext, DeleteCandidateContext)) and not decision.topic_changed:
        history = [message.model_dump() for message in request.context.recent_messages]
    history.append({"role": "user", "text": request.text})
    if status == "needs_clarification":
        history.append({"role": "assistant", "text": message})
    if decision.intent == "on_delete":
        if isinstance(decision.action, DeleteSelectionAction):
            if decision.topic_changed or not isinstance(request.context, DeleteCandidateContext):
                raise InvalidModelOutput("Delete confirmation needs saved candidates")
            candidates = {candidate.number: candidate.id for candidate in request.context.candidates}
            if any(number not in candidates for number in decision.action.selected_numbers):
                raise InvalidModelOutput("Delete selection contains an unknown candidate number")
            values["action"] = DeleteConfirmedAction(
                phase="delete_confirmed",
                task_ids=[candidates[number] for number in decision.action.selected_numbers],
            ).model_dump(mode="json")
    # No implicit merging: a new action/draft is the full current topic state.
    response = {
        "user_id": request.user_id,
        "task_id": request.task_id,
        "status": status,
        "message": message,
        "action": values["action"],
        "context": {"recent_messages": history, "draft": values["draft"]},
    }
    if decision.intent != "on_delete":
        response["intent"] = decision.intent
    return JOB_RESPONSE_ADAPTER.validate_python(response)
