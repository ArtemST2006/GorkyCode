"""The result endpoint never needs a model, even when one is configured."""

import asyncio
from types import SimpleNamespace

from fastapi import FastAPI
import httpx
import pytest

from app.api.exception_handlers import register_exception_handlers
from app.api.routes.respond import router


USER_ID = "b80e93e1-9945-48e0-8460-3ee68bcfb402"
TASK_ID = "7b612951-57ae-4ac4-a93c-42fa9c9e8359"


def payload():
    return {
        "user_id": USER_ID, "task_id": TASK_ID,
        "text": "Найди встречи", "timezone": "Europe/Moscow",
        "intent": "search_events", "status": "ok", "data": {"items": []},
    }


def client_response(value, *, configured_gateway=True):
    class ForbiddenGateway:
        async def complete(self, *args, **kwargs):
            pytest.fail("Result narration must never call the model")

    app = FastAPI()
    app.include_router(router)
    register_exception_handlers(app)
    if configured_gateway:
        app.state.job_service = SimpleNamespace(gateway=ForbiddenGateway())

    async def send():
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=app), base_url="http://test"
        ) as client:
            return await client.post("/api/respond", json=value)

    return asyncio.run(send())


@pytest.mark.parametrize("configured_gateway", [True, False])
def test_respond_preserves_original_ids_without_a_model(configured_gateway):
    response = client_response(payload(), configured_gateway=configured_gateway)
    assert response.status_code == 200
    assert response.json() == {
        "user_id": USER_ID, "task_id": TASK_ID,
        "message": "Записей не найдено. Уточните запрос.",
    }


@pytest.mark.parametrize("intent,status,data,expected", [
    ("create_event", "ok", {"event": {}}, "Запись создана."),
    ("list_events", "ok", {"items": [{}, {}]}, "Найдено записей: 2."),
    ("search_events", "ok", {"items": [{} for _ in range(60)]}, "Найдено записей: 60."),
    ("on_delete", "candidates", {"candidates": [{"number": 7}, {"number": 42}]},
     "Найдено записей: 2. Удаление ещё не выполнено. Выберите номера для подтверждения: 7, 42."),
    ("on_update", "update_candidates", {"candidates": [{"number": 9}], "changes": {"title": "x"}},
     "Найдено записей: 1. Изменение ещё не выполнено. Предлагается изменить: название. Выберите номера для подтверждения: 9."),
    ("on_delete", "deleted", {"deleted_events": []}, "Удалено записей: 0."),
    ("on_update", "updated", {"updated_events": [{}]}, "Обновлено записей: 1."),
])
def test_all_supported_operation_variants(intent, status, data, expected, assert_contract):
    value = payload() | {"intent": intent, "status": status, "data": data}
    assert_contract(value, "RespondRequest")
    response = client_response(value)
    assert response.status_code == 200
    assert response.json()["message"] == expected
    assert_contract(response.json(), "RespondResponse")


@pytest.mark.parametrize("patch", [
    {"status": "deleted"},
    {"intent": "create_event", "data": {"items": []}},
    {"data": {"items": {}, "event": {}}},
    {"data": {"items": ["bad"]}},
    {"data": {"items": [], "unknown": "injection"}},
    {"intent": "on_update", "status": "update_candidates", "data": {"candidates": []}},
])
def test_invalid_result_variants_are_rejected(patch):
    response = client_response(payload() | patch)
    assert response.status_code == 400
    assert response.json()["code"] == "invalid_request"


def test_operation_id_is_unchanged():
    app = FastAPI()
    app.include_router(router)
    assert app.openapi()["paths"]["/api/respond"]["post"]["operationId"] == "respondToResult"
