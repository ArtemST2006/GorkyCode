"""Task status wording must enter on_update even when the model misroutes it."""

import asyncio
import json

import pytest
from fastapi import FastAPI
import httpx

from app.api.dependencies import get_job_service
from app.api.routes.jobs import router as jobs_router
from app.core.config import Settings
from app.schemas.job import JobRequest, UpdateFindCandidatesResponse
from app.services.jobs import JobService
from app.services.status_update_recovery import recover_task_status_update
from app.security.guard_schemas import GuardVerdict


USER_ID = "b80e93e1-9945-48e0-8460-3ee68bcfb402"
TASK_ID = "7b612951-57ae-4ac4-a93c-42fa9c9e8359"


class AllowGuard:
    async def verify(self, _request, *, intent, proposal):
        return GuardVerdict(decision="allow", reason_code="consistent_request")


def request(text: str) -> JobRequest:
    return JobRequest.model_validate({
        "user_id": USER_ID, "task_id": TASK_ID, "text": text,
        "request_time": "2026-10-30T12:00:00+03:00",
        "timezone": "Europe/Moscow", "context": {},
    })


@pytest.mark.parametrize("phrase,status", [
    ("Переведи статус задачи в выполнение", "in_progress"),
    ("Поставь задачу в работу", "in_progress"),
    ("Пометь задачу как в работе", "in_progress"),
    ("Отметь задачу как завершено", "completed"),
    ("Поменяй статус задачи на выполнено", "completed"),
    ("Установи статус задачи в активные", "active"),
])
def test_status_change_synonyms_recover_update_intent(phrase, status):
    decision = recover_task_status_update(phrase)
    assert decision is not None
    assert decision.intent == "on_update"
    assert decision.action.changes.status == status
    assert decision.action.record_types == ["task"]


@pytest.mark.parametrize("phrase", [
    "Переведи задачу на русский язык",
    "Не переводи статус задачи в выполнение",
    "Можно перевести статус задачи в выполнение?",
    "Переведи встречу с Иваном на завтра",
])
def test_non_commands_do_not_trigger_recovery(phrase):
    assert recover_task_status_update(phrase) is None


@pytest.mark.parametrize("model_output", [
    "not json",
    json.dumps({
        "status": "unsupported", "intent": None, "message": None,
        "action": None, "draft": None, "topic_changed": False,
    }),
    json.dumps({
        "status": "ready", "intent": "chat", "message": "Могу помочь.",
        "action": None, "draft": None, "topic_changed": False,
    }),
    json.dumps({
        "status": "ready", "intent": "on_update", "message": "Меняю статус.",
        "action": {"phase": "find_candidates", "title_query": None,
                   "date_from": None, "date_to_exclusive": None,
                   "time_from": None, "time_to_exclusive": None,
                   "record_types": ["task"], "changes": {"status": "completed"}},
        "draft": None, "topic_changed": False,
    }),
])
def test_broken_or_wrong_model_result_still_finds_task_candidates(model_output):
    class Gateway:
        calls = 0

        async def complete(self, _messages):
            self.calls += 1
            return model_output

    gateway = Gateway()
    result = asyncio.run(JobService(Settings(job_timeout_seconds=5), gateway, AllowGuard()).process(
        request("Переведи статус задачи в выполнение")))
    assert isinstance(result, UpdateFindCandidatesResponse)
    assert result.action.changes.status == "in_progress"
    assert result.action.record_types == ["task"]
    assert gateway.calls == 1


def test_valid_model_keeps_its_specific_task_filter():
    class Gateway:
        async def complete(self, _messages):
            return json.dumps({
                "status": "ready", "intent": "on_update", "message": "Ищу проект.",
                "action": {"phase": "find_candidates", "title_query": "проект C",
                           "date_from": None, "date_to_exclusive": None,
                           "time_from": None, "time_to_exclusive": None,
                           "record_types": ["task"], "changes": {"status": "in_progress"}},
                "draft": None, "topic_changed": False,
            })

    result = asyncio.run(JobService(Settings(job_timeout_seconds=5), Gateway(), AllowGuard()).process(
        request("Переведи задачу по проекту C в выполнение")))
    assert isinstance(result, UpdateFindCandidatesResponse)
    assert result.action.title_query == "проект C"


def test_http_response_exposes_update_intent_after_recovery():
    class BrokenGateway:
        async def complete(self, _messages):
            return "broken JSON"

    application = FastAPI()
    application.include_router(jobs_router)
    async def service():
        return JobService(Settings(job_timeout_seconds=5), BrokenGateway(), AllowGuard())

    application.dependency_overrides[get_job_service] = service

    async def check():
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=application),
                                    base_url="http://test") as client:
            return await client.post("/api/job", json=request(
                "Переведи статус задачи в выполнение").model_dump(mode="json"))

    response = asyncio.run(check())
    assert response.status_code == 200
    assert response.headers["X-Task-Intent"] == "on_update"
    assert response.json()["action"]["changes"]["status"] == "in_progress"
