import json
from types import SimpleNamespace

from fastapi import FastAPI
from fastapi.testclient import TestClient

from app.api.dependencies import get_job_service
from app.api.exception_handlers import register_exception_handlers
from app.api.routes.respond import router


def client_for(completion):
    class Gateway:
        async def complete(self, messages):
            self.messages = messages
            return completion
    gateway = Gateway()
    app = FastAPI()
    app.include_router(router)
    register_exception_handlers(app)
    app.dependency_overrides[get_job_service] = lambda: SimpleNamespace(gateway=gateway)
    return TestClient(app), gateway


def payload():
    return {
        "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
        "task_id": "7b612951-57ae-4ac4-a93c-42fa9c9e8359",
        "text": "Найди встречи", "timezone": "Europe/Moscow",
        "intent": "search_events", "status": "ok", "data": {"items": []},
    }


def test_respond_uses_database_result_and_returns_original_ids():
    client, gateway = client_for('{"message":"Встреч не нашлось. Попробуем другой период?"}')
    response = client.post("/api/respond", json=payload())
    assert response.status_code == 200
    assert response.json()["user_id"] == payload()["user_id"]
    assert response.json()["task_id"] == payload()["task_id"]
    assert json.loads(gateway.messages[-1]["content"])["data"] == {"items": []}


def test_respond_rejects_actions_and_empty_messages():
    for value in ('{"message":""}', '{"message":"   "}', '{"message":"ok","action":{"delete":true}}', 'not json'):
        client, _ = client_for(value)
        assert client.post("/api/respond", json=payload()).status_code == 502


def test_respond_rejects_incompatible_result():
    client, _ = client_for('{"message":"ok"}')
    assert client.post("/api/respond", json=payload() | {"status": "deleted"}).status_code == 400


def test_provider_receives_display_fields_only_and_bounded_lists():
    client, gateway = client_for('{"message":"Нашёл 60 записей."}')
    value = payload() | {"data": {"items": [
        {"id": "internal-id", "user_id": "internal-owner", "title": "Встреча", "access_token": "must-not-leave"}
        for _ in range(60)
    ]}}
    assert client.post("/api/respond", json=value).status_code == 200
    sent = json.loads(gateway.messages[-1]["content"])
    assert sent["total_counts"]["items"] == 60
    assert len(sent["data"]["items"]) == 50
    assert sent["data"]["items"][0] == {"title": "Встреча"}
    assert "user_id" not in sent and "task_id" not in sent
