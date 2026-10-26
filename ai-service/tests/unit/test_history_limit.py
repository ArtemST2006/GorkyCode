import json

from app.llm.messages import build_messages
from app.llm.schemas import ModelDecision
from app.schemas.job import JobRequest
from app.services.context import build_job_response


def test_long_history_is_bounded_without_losing_the_draft():
    history = [{"role": "user", "text": f"Сообщение {i}"} for i in range(40)]
    request = JobRequest.model_validate({
        "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
        "task_id": "7b612951-57ae-4ac4-a93c-42fa9c9e8359",
        "text": "Завтра",
        "request_time": "2026-10-26T12:00:00+03:00",
        "timezone": "Europe/Moscow",
        "context": {"recent_messages": history, "draft": {"record_type": "meeting", "title": "Встреча с Иваном"}},
    })
    payload = json.loads(build_messages(request, {})[-1]["content"])
    assert len(payload["context"]["recent_messages"]) == 20
    assert payload["context"]["recent_messages"][0]["text"] == "Сообщение 20"
    assert payload["context"]["draft"]["title"] == "Встреча с Иваном"
    decision = ModelDecision.model_validate({
        "status": "needs_clarification", "intent": "create_event",
        "message": "Во сколько?", "action": None,
        "draft": {"record_type": "meeting", "title": "Встреча с Иваном", "date": "2026-10-27"},
        "topic_changed": False,
    })
    response = build_job_response(request, decision)
    assert len(response.context.recent_messages) == 20
    assert response.context.recent_messages[-1].text == "Во сколько?"
    assert response.context.draft.title == "Встреча с Иваном"
