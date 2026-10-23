"""Construct public dialogue history and responses in application code."""

from app.llm.schemas import ModelDecision
from app.schemas.context import StoredContext
from app.schemas.job import JOB_RESPONSE_ADAPTER, JobRequest, JobResponse


def build_job_response(request: JobRequest, decision: ModelDecision) -> JobResponse:
    # Empty/date-only drafts match several internal union branches. Re-validate
    # the JSON shape against the selected intent, not the branch Python chose.
    values = decision.model_dump(mode="json", exclude_unset=True)
    history = []
    if isinstance(request.context, StoredContext) and not decision.topic_changed:
        history = [message.model_dump() for message in request.context.recent_messages]
    history.append({"role": "user", "text": request.text})
    if decision.status == "needs_clarification":
        history.append({"role": "assistant", "text": decision.message})
    # No implicit merging: a new action/draft is the full current topic state.
    return JOB_RESPONSE_ADAPTER.validate_python(
        {
            "user_id": request.user_id,
            "task_id": request.task_id,
            "status": decision.status,
            "intent": decision.intent,
            "message": decision.message,
            "action": values["action"],
            "context": {"recent_messages": history, "draft": values["draft"]},
        }
    )
