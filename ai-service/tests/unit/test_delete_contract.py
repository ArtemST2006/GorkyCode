"""Offline checks for the two-stage on_delete wire contract."""

import json
import unittest
from unittest.mock import patch

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from pydantic import ValidationError

from app.api.dependencies import get_job_service
from app.api.routes.jobs import router as jobs_router
from app.core.config import Settings
from app.core.errors import InvalidModelOutput, ServiceError
from app.llm.schemas import ModelDecision
from app.schemas.delete_event import DeleteCandidate, DeleteFindCandidatesAction
from app.schemas.job import (
    DeleteConfirmedResponse,
    DeleteFindCandidatesResponse,
    DeleteNeedsClarificationResponse,
    UpdateConfirmedResponse,
    JOB_RESPONSE_ADAPTER,
    JobRequest,
)
from app.services.context import build_job_response, explicit_selection_numbers
from app.services.jobs import JobService


USER_ID = "b80e93e1-9945-48e0-8460-3ee68bcfb402"
TASK_ID = "70ce187e-a2a6-4499-91a0-5c61e5920b51"
FIRST_ID = "2d7f8e0b-2fae-4b46-835a-675f748f3b11"
FIFTH_ID = "ee79bdbf-f5a8-4204-a821-4a4ab21d14f6"


def request(text: str, context: dict | None = None) -> JobRequest:
    return JobRequest.model_validate(
        {
            "user_id": USER_ID,
            "task_id": TASK_ID,
            "text": text,
            "request_time": "2026-10-23T14:03:00+03:00",
            "timezone": "Europe/Moscow",
            "context": {} if context is None else context,
        }
    )


def decision(**changes) -> ModelDecision:
    values = {
        "status": "ready",
        "intent": "on_delete",
        "message": "Подбираю записи для удаления.",
        "action": {
            "phase": "find_candidates",
            "title_query": "Иван",
            "date_from": None,
            "date_to_exclusive": None,
            "time_from": None,
            "time_to_exclusive": None,
            "record_types": ["meeting"],
        },
        "draft": None,
        "topic_changed": False,
    }
    return ModelDecision.model_validate(values | changes)


def candidate(number: int, task_id: str) -> dict:
    return {
        "number": number,
        "id": task_id,
        "record_type": "meeting",
        "title": "Встреча с Иваном",
        "date": "2026-10-23",
        "time": "15:00",
        "status": "active",
    }


CANDIDATE_CONTEXT = {
    "recent_messages": [
        {"role": "user", "text": "Удали встречи с Иваном сегодня"},
        {"role": "assistant", "text": "Нашёл пять встреч. Какие удалить?"},
    ],
    "draft": {"title_query": "Иван", "date_from": "2026-10-23"},
    "candidates": [candidate(1, FIRST_ID), candidate(5, FIFTH_ID)],
}


def test_delete_candidate_progress_status_is_task_only():
    values = candidate(1, FIRST_ID) | {"record_type": "task", "status": "in_progress"}
    assert DeleteCandidate.model_validate(values).status == "in_progress"
    for record_type in ("meeting", "reminder"):
        with pytest.raises(ValidationError):
            DeleteCandidate.model_validate(values | {"record_type": record_type})


class DeleteContractTest(unittest.TestCase):
    def test_numbers_inside_questions_or_times_are_not_confirmation(self):
        selection = decision(action={"phase": "delete_confirmed", "selected_numbers": [1]})
        for text in ("Что в записи №1?", "Покажи №1", "Удали №1?", "В 1:00", "1.5", "Не надо, №1"):
            with self.subTest(text=text):
                result = build_job_response(request(text, CANDIDATE_CONTEXT), selection)
                self.assertNotIsInstance(result, DeleteConfirmedResponse)

    def test_update_must_not_confirm_a_delete_command(self):
        context = CANDIDATE_CONTEXT | {
            "draft": {"title_query": "Иван", "changes": {"time": "16:00"}}
        }
        selected = decision(intent="on_update", action={"phase": "update_confirmed", "selected_numbers": [1]})
        result = build_job_response(request("Удали №1", context), selected)
        self.assertNotIsInstance(result, UpdateConfirmedResponse)

    def test_spoken_candidate_numbers_are_explicit_and_negation_is_not(self):
        shown = {1, 5}
        self.assertEqual(explicit_selection_numbers("первую", shown), [1])
        self.assertEqual(explicit_selection_numbers("Удали первую и пятую", shown), [1, 5])
        self.assertEqual(explicit_selection_numbers("Удали №5 и №1", shown), [5, 1])
        self.assertEqual(explicit_selection_numbers("удали все показанные", shown), [1, 5])
        self.assertIsNone(explicit_selection_numbers("не удаляй первую", shown))
        self.assertIsNone(explicit_selection_numbers("удали задачу 1 ноября", shown))

    def test_http_header_only_for_delete(self):
        class FakeService:
            def __init__(self, result):
                self.result = result

            async def process(self, _payload):
                return self.result

        application = FastAPI()
        application.include_router(jobs_router)
        delete_result = build_job_response(request("Удали встречи с Иваном"), decision())
        application.dependency_overrides[get_job_service] = lambda: FakeService(delete_result)
        with TestClient(application) as client:
            response = client.post("/api/job", json=request("Удали встречи с Иваном").model_dump(mode="json"))
            self.assertEqual(response.status_code, 200)
            self.assertEqual(response.headers["x-task-intent"], "on_delete")
            self.assertNotIn("intent", response.json())
            self.assertEqual(response.json()["action"]["phase"], "find_candidates")

            legacy = build_job_response(
                request("Что у меня завтра?"),
                ModelDecision.model_validate({
                    "status": "ready", "intent": "list_events", "message": "Проверил события.",
                    "action": {"date_from": "2026-10-24", "date_to_exclusive": "2026-10-25"},
                    "draft": None, "topic_changed": False,
                }),
            )
            application.dependency_overrides[get_job_service] = lambda: FakeService(legacy)
            response = client.post("/api/job", json=request("Что у меня завтра?").model_dump(mode="json"))
            self.assertEqual(response.status_code, 200)
            self.assertNotIn("x-task-intent", response.headers)
            self.assertEqual(response.json()["intent"], "list_events")

    def test_clarification_has_no_json_intent(self):
        result = build_job_response(
            request("Удали запись"),
            decision(
                status="needs_clarification",
                message="Какую запись нужно удалить?",
                action=None,
                draft={},
            ),
        )
        self.assertIsInstance(result, DeleteNeedsClarificationResponse)
        body = result.model_dump(mode="json", exclude_unset=True)
        self.assertNotIn("intent", body)
        self.assertEqual(body["context"]["draft"], {})
        self.assertEqual(body["context"]["recent_messages"][-1]["role"], "assistant")
        JOB_RESPONSE_ADAPTER.validate_python(body)

    def test_find_candidates_has_all_nullable_filters(self):
        result = build_job_response(request("Удали встречи с Иваном"), decision())
        self.assertIsInstance(result, DeleteFindCandidatesResponse)
        body = result.model_dump(mode="json", exclude_unset=True)
        self.assertNotIn("intent", body)
        self.assertEqual(
            set(body["action"]),
            {
                "phase",
                "title_query",
                "date_from",
                "date_to_exclusive",
                "time_from",
                "time_to_exclusive",
                "record_types",
            },
        )
        self.assertIsNone(body["action"]["date_from"])
        self.assertIsNone(body["context"]["draft"])
        JOB_RESPONSE_ADAPTER.validate_python(body)

    def test_selection_maps_numbers_to_saved_ids_in_order(self):
        result = build_job_response(
            request("Удали №5 и №1", CANDIDATE_CONTEXT),
            decision(
                message="Удаляю выбранные записи.",
                action={"phase": "delete_confirmed", "selected_numbers": [5, 1]},
            ),
        )
        self.assertIsInstance(result, DeleteConfirmedResponse)
        body = result.model_dump(mode="json", exclude_unset=True)
        self.assertNotIn("intent", body)
        self.assertEqual(body["action"], {"phase": "delete_confirmed", "task_ids": [FIFTH_ID, FIRST_ID]})
        self.assertEqual(body["context"]["recent_messages"][-1]["text"], "Удали №5 и №1")
        JOB_RESPONSE_ADAPTER.validate_python(body)

    def test_selection_requires_shown_candidates(self):
        selection = decision(action={"phase": "delete_confirmed", "selected_numbers": [1]})
        for current, changed in [({}, False), (CANDIDATE_CONTEXT, True)]:
            with self.subTest(context=current, changed=changed):
                with self.assertRaises(InvalidModelOutput):
                    build_job_response(
                        request("Удали №1", current),
                        selection.model_copy(update={"topic_changed": changed}),
                    )
        with self.assertRaises(InvalidModelOutput):
            build_job_response(
                request("Удали №2", CANDIDATE_CONTEXT),
                decision(action={"phase": "delete_confirmed", "selected_numbers": [2]}),
            )

    def test_unclear_choice_repeats_search_so_candidates_are_not_lost(self):
        result = build_job_response(
            request("А какие были?", CANDIDATE_CONTEXT),
            decision(
                status="needs_clarification",
                message="Какие записи удалить?",
                action=None,
                draft={},
            ),
        )
        self.assertIsInstance(result, DeleteFindCandidatesResponse)
        self.assertEqual(result.action.title_query, "Иван")
        self.assertEqual(result.action.date_from, "2026-10-23")
        self.assertIsNone(result.context.draft)

    def test_invalid_actions_and_candidates_are_rejected(self):
        for changes in [
            {"action": {"phase": "delete_confirmed", "selected_numbers": []}},
            {"action": {"phase": "delete_confirmed", "selected_numbers": [1, 1]}},
            {"action": {"phase": "find_candidates", "title_query": None,
                        "date_from": None, "date_to_exclusive": None, "time_from": None,
                        "time_to_exclusive": None, "record_types": None}},
        ]:
            with self.subTest(changes=changes), self.assertRaises(ValidationError):
                decision(**changes)
        with self.assertRaises(ValidationError):
            DeleteFindCandidatesAction.model_validate({"phase": "find_candidates", "title_query": "Иван"})
        duplicate = CANDIDATE_CONTEXT | {
            "candidates": [candidate(1, FIRST_ID), candidate(1, FIFTH_ID)]
        }
        with self.assertRaises(ValidationError):
            request("Удали №1", duplicate)


class DeleteJobServiceTest(unittest.IsolatedAsyncioTestCase):
    async def test_fast_path_does_not_change_when_user_asks_to_delete(self):
        class Gateway:
            calls = 0

            async def complete(self, _messages):
                self.calls += 1
                return json.dumps({
                    "intent": "on_update", "status": "needs_clarification",
                    "message": "Изменить или удалить?", "action": None,
                    "draft": {}, "topic_changed": False,
                })

        context = CANDIDATE_CONTEXT | {
            "draft": {"title_query": "Иван", "changes": {"time": "16:00"}}
        }
        gateway = Gateway()
        result = await JobService(Settings(job_timeout_seconds=5), gateway).process(
            request("Удали №1", context)
        )
        self.assertEqual(gateway.calls, 1)
        self.assertNotIsInstance(result, UpdateConfirmedResponse)

    async def test_spoken_selection_uses_saved_candidates_without_model(self):
        class UnusedGateway:
            async def complete(self, _messages):
                raise AssertionError("Explicit selection should not call the model")

        gateway = UnusedGateway()
        result = await JobService(Settings(job_timeout_seconds=5), gateway).process(
            request("первую", CANDIDATE_CONTEXT)
        )
        self.assertIsInstance(result, DeleteConfirmedResponse)
        self.assertEqual(result.action.task_ids, [FIRST_ID])

    async def test_spoken_update_selection_uses_saved_candidates_without_model(self):
        class UnusedGateway:
            async def complete(self, _messages):
                raise AssertionError("Explicit selection should not call the model")

        context = CANDIDATE_CONTEXT | {
            "draft": {"title_query": "Иван", "changes": {"time": "16:00"}}
        }
        result = await JobService(Settings(job_timeout_seconds=5), UnusedGateway()).process(
            request("первую", context)
        )
        self.assertIsInstance(result, UpdateConfirmedResponse)
        self.assertEqual(result.action.record_ids, [FIRST_ID])

    @patch("app.services.jobs.explicit_selection_numbers", return_value=None)
    async def test_invalid_selected_number_is_retried_once(self, _fast_path):
        class Gateway:
            def __init__(self, completions):
                self.completions = list(completions)
                self.calls = 0

            async def complete(self, _messages):
                self.calls += 1
                return json.dumps(self.completions.pop(0), ensure_ascii=False)

        invalid = decision(
            action={"phase": "delete_confirmed", "selected_numbers": [2]},
            message="Удаляю выбранные записи.",
        ).model_dump(mode="json")
        valid = decision(
            action={"phase": "delete_confirmed", "selected_numbers": [1]},
            message="Удаляю выбранные записи.",
        ).model_dump(mode="json")
        gateway = Gateway([invalid, valid])
        service = JobService(Settings(job_timeout_seconds=5), gateway)
        result = await service.process(request("Удали запись №1 из найденных кандидатов", CANDIDATE_CONTEXT))
        self.assertIsInstance(result, DeleteConfirmedResponse)
        self.assertEqual(result.action.task_ids, [FIRST_ID])
        self.assertEqual(gateway.calls, 2)

        rejected = Gateway([invalid, invalid])
        with self.assertRaises(ServiceError) as raised:
            await JobService(Settings(job_timeout_seconds=5), rejected).process(
                request("Удали запись №1 из найденных кандидатов", CANDIDATE_CONTEXT)
            )
        self.assertEqual(raised.exception.code, "model_invalid_output")
        self.assertEqual(rejected.calls, 2)


def test_delete_responses_match_root_openapi(assert_contract):
    scenarios = [
        (request("Удали запись"), decision(
            status="needs_clarification", message="Какую запись нужно удалить?",
            action=None, draft={},
        )),
        (request("Удали встречи с Иваном"), decision()),
        (request("Удали №1", CANDIDATE_CONTEXT), decision(
            message="Удаляю выбранные записи.",
            action={"phase": "delete_confirmed", "selected_numbers": [1]},
        )),
    ]
    for incoming, extracted in scenarios:
        assert_contract(incoming.model_dump(mode="json", exclude_unset=True), "JobRequest")
        body = build_job_response(incoming, extracted).model_dump(mode="json", exclude_unset=True)
        assert_contract(body, "JobResponse")


def test_root_delete_examples_are_supported(contract):
    operation = contract["paths"]["/api/job"]["post"]
    JobRequest.model_validate(operation["requestBody"]["content"]["application/json"]
                              ["examples"]["deleteSelection"]["value"])
    examples = operation["responses"]["200"]["content"]["application/json"]["examples"]
    for name in (
        "deleteNeedsClarification", "deleteFindCandidates", "deleteFindCandidatesByTime",
        "deleteConfirmed",
    ):
        result = JOB_RESPONSE_ADAPTER.validate_python(examples[name]["value"])
        assert "intent" not in result.model_dump(mode="json", exclude_unset=True)


if __name__ == "__main__":
    unittest.main()
