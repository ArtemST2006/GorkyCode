"""Partial updates must search candidates before asking for unchanged fields."""

import asyncio

import pytest

from app.core.config import Settings
from app.llm.schemas import ModelDecision
from app.schemas.job import JobRequest, UpdateFindCandidatesResponse
from app.services.context import build_job_response
from app.services.jobs import JobService


USER_ID = "b80e93e1-9945-48e0-8460-3ee68bcfb402"
TASK_ID = "7b612951-57ae-4ac4-a93c-42fa9c9e8359"


def request(context=None):
    return JobRequest.model_validate({
        "user_id": USER_ID, "task_id": TASK_ID,
        "text": "Надо изменить вообщем встречу с Таней на 19 часов",
        "request_time": "2026-10-27T12:00:00+03:00",
        "timezone": "Europe/Moscow", "context": context or {},
    })


def clarification(draft, **kwargs):
    return ModelDecision.model_validate({
        "status": "needs_clarification", "intent": "on_update",
        "message": "На какую дату и время (19:00) перенести встречу с Таней?",
        "action": None, "draft": draft, "topic_changed": False, **kwargs,
    })


@pytest.mark.parametrize("changes", [
    {"time": "19:00"}, {"date": "2026-10-28"},
    {"title": "Ужин с Таней"}, {"status": "completed"},
])
def test_one_change_is_enough_to_search(changes, assert_contract):
    decision = clarification({
        "title_query": "с Таней", "record_types": ["meeting"], "changes": changes,
    })

    class Gateway:
        calls = 0

        async def complete(self, _messages):
            self.calls += 1
            return decision.model_dump_json(exclude_unset=True)

    gateway = Gateway()
    result = asyncio.run(JobService(Settings(job_timeout_seconds=5), gateway).process(request()))
    assert isinstance(result, UpdateFindCandidatesResponse)
    assert gateway.calls == 1  # No paid retry for an already complete patch.
    payload = result.model_dump(mode="json", exclude_unset=True)
    assert payload["action"]["phase"] == "find_candidates"
    assert payload["action"]["changes"] == changes
    assert payload["action"]["date_from"] is None
    assert payload["action"]["time_from"] is None
    assert payload["action"]["record_types"] == ["meeting"]
    assert payload["context"]["draft"] is None
    assert payload["context"]["recent_messages"][-1]["role"] == "user"
    assert payload["user_id"] == USER_ID
    assert payload["task_id"] == TASK_ID
    assert "intent" not in payload
    assert_contract(payload, "UpdateFindCandidates")


@pytest.mark.parametrize("draft", [
    {"title_query": "с Таней"},
    {"title_query": "с Таней", "changes": {}},
    {"changes": {"time": "19:00"}},
    {"date_from": "2026-10-28", "date_to_exclusive": "2026-10-27",
     "changes": {"time": "19:00"}},
])
def test_incomplete_or_invalid_patch_still_needs_clarification(draft):
    result = build_job_response(request(), clarification(draft))
    assert result.status == "needs_clarification"
    assert result.action is None


def test_new_topic_does_not_take_old_filters_or_changes():
    context = {
        "recent_messages": [{"role": "user", "text": "Перенеси встречу с Иваном"}],
        "draft": {"title_query": "с Иваном", "changes": {"date": "2026-11-01"}},
    }
    decision = clarification({
        "title_query": "с Таней", "changes": {"time": "19:00"},
    }, topic_changed=True)
    result = build_job_response(request(context), decision)
    assert result.action.title_query == "с Таней"
    assert result.action.changes.model_dump(exclude_unset=True) == {"time": "19:00"}
    assert len(result.context.recent_messages) == 1


def test_partial_update_does_not_relax_creation_requirements():
    decision = clarification({
        "record_type": "meeting", "title": "Встреча с Таней", "time": "19:00",
    }, intent="create_event")
    result = build_job_response(request(), decision)
    assert result.status == "needs_clarification"
    assert result.action is None


def test_null_change_is_not_enough_to_become_ready():
    decision = clarification({"title_query": "с Таней", "changes": {"time": None}})
    result = build_job_response(request(), decision)
    assert result.status == "needs_clarification"
    assert result.action is None
