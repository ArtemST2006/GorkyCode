"""Contract examples and important rejection boundaries; no provider calls."""

from copy import deepcopy
import unittest

from pydantic import TypeAdapter, ValidationError

from app.schemas.create_event import CreateEventDraft, TaskAction
from app.schemas.job import JOB_RESPONSE_ADAPTER, JobRequest
from app.schemas.list_events import ListEventsAction, ListEventsDraft
from app.schemas.problem import Problem
from app.schemas.search_events import SearchEventsAction, SearchEventsDraft


USER_ID = "b80e93e1-9945-48e0-8460-3ee68bcfb402"
TASK_ID = "7b612951-57ae-4ac4-a93c-42fa9c9e8359"
MESSAGES = [
    {"role": "user", "text": "Назначь встречу с Иваном завтра"},
    {"role": "assistant", "text": "В какое время?"},
]
DRAFT = {"record_type": "meeting", "title": "Встреча с Иваном", "date": "2026-10-19"}
FIRST_REQUEST = {
    "user_id": USER_ID,
    "task_id": TASK_ID,
    "text": "Назначь встречу с Иваном завтра",
    "request_time": "2026-10-18T12:00:00+03:00",
    "timezone": "Europe/Moscow",
    "context": {},
}
CLARIFICATION = {
    "user_id": USER_ID,
    "task_id": TASK_ID,
    "status": "needs_clarification",
    "intent": "create_event",
    "message": "В какое время?",
    "action": None,
    "context": {"recent_messages": MESSAGES, "draft": DRAFT},
}


def ready(intent, action, messages):
    return {
        "user_id": USER_ID,
        "task_id": TASK_ID,
        "status": "ready",
        "intent": intent,
        "message": "Запрос выполнен.",
        "action": action,
        "context": {"recent_messages": messages, "draft": None},
    }


class ContractSchemasTest(unittest.TestCase):
    def test_all_three_request_examples(self):
        followup = deepcopy(FIRST_REQUEST)
        followup.update(
            text="В 15:00",
            request_time="2026-10-18T12:00:20+03:00",
            context={"recent_messages": MESSAGES, "draft": DRAFT},
        )
        switch = deepcopy(followup)
        switch.update(
            text="Создай задачу купить молоко завтра в 18:00",
            request_time="2026-10-18T12:01:00+03:00",
        )
        for value in [FIRST_REQUEST, followup, switch]:
            with self.subTest(text=value["text"]):
                parsed = JobRequest.model_validate(value)
                self.assertEqual(parsed.context.model_dump(exclude_unset=True), value["context"])

    def test_all_five_response_examples_roundtrip(self):
        examples = [
            CLARIFICATION,
            ready(
                "create_event",
                {**DRAFT, "time": "15:00"},
                MESSAGES + [{"role": "user", "text": "В 15:00"}],
            ),
            ready(
                "list_events",
                {"date_from": "2026-10-19", "date_to_exclusive": "2026-10-20"},
                [{"role": "user", "text": "Что у меня завтра?"}],
            ),
            ready(
                "search_events",
                {"query": "Иван", "record_types": ["meeting"]},
                [{"role": "user", "text": "Найди встречи с Иваном"}],
            ),
            ready(
                "create_event",
                {
                    "record_type": "task",
                    "title": "Купить молоко",
                    "date": "2026-10-19",
                    "time": "18:00",
                },
                [{"role": "user", "text": "Создай задачу купить молоко завтра в 18:00"}],
            ),
        ]
        for value in examples:
            with self.subTest(intent=value["intent"], status=value["status"]):
                parsed = JOB_RESPONSE_ADAPTER.validate_python(value)
                self.assertEqual(parsed.model_dump(mode="json", exclude_unset=True), value)

    def test_uuid_case_is_preserved(self):
        value = {**FIRST_REQUEST, "user_id": USER_ID.upper()}
        self.assertEqual(JobRequest.model_validate(value).user_id, USER_ID.upper())

    def test_rejects_invalid_request_fields(self):
        for changes in [
            {"user_id": "not-uuid"},
            {"text": ""},
            {"request_time": "2026-10-18T12:00:00"},
            {"timezone": "Missing/Timezone"},
            {"request_time": 1792314000},
            {"request_time": "2026-10-18 12:00:00+03:00"},
            {"context": {"draft": {}}},
            {"context": {"unknown": 1}},
            {"context": {"recent_messages": [], "draft": {}}},
            {"context": {"recent_messages": MESSAGES, "draft": None}},
            {"unexpected": True},
        ]:
            with self.subTest(changes=changes), self.assertRaises(ValidationError):
                JobRequest.model_validate({**FIRST_REQUEST, **changes})

    def test_optional_properties_accept_absence_but_reject_null(self):
        for model in [CreateEventDraft, ListEventsDraft, SearchEventsDraft]:
            self.assertEqual(model().model_dump(exclude_unset=True), {})
            for name in model.model_fields:
                with (
                    self.subTest(model=model.__name__, field=name),
                    self.assertRaises(ValidationError),
                ):
                    model.model_validate({name: None})
        for changes in [{"record_types": None}, {"date_from": None}, {"query": "   "}]:
            with self.subTest(changes=changes), self.assertRaises(ValidationError):
                SearchEventsAction.model_validate({"query": "Иван", **changes})

    def test_calendar_dates_and_local_times(self):
        good = {"record_type": "task", "title": "Задача", "date": "2028-02-29", "time": "23:59"}
        TaskAction.model_validate(good)
        for changes in [
            {"date": "2026-02-29"},
            {"date": "20261019"},
            {"date": "2026-10-19T00:00:00"},
            {"time": "9:00"},
            {"time": "24:00"},
            {"time": "15:00:00"},
            {"time": "15:00\n"},
        ]:
            with self.subTest(changes=changes), self.assertRaises(ValidationError):
                TaskAction.model_validate({**good, **changes})
        for field in ["title", "date", "record_type"]:
            incomplete = {key: value for key, value in good.items() if key != field}
            with self.subTest(missing=field), self.assertRaises(ValidationError):
                TaskAction.model_validate(incomplete)
        without_time = {key: value for key, value in good.items() if key != "time"}
        self.assertIsNone(TaskAction.model_validate(without_time).time)

    def test_record_types_are_nonempty_and_unique(self):
        for types in [[], ["meeting", "meeting"], ["other"]]:
            with self.subTest(types=types), self.assertRaises(ValidationError):
                SearchEventsAction(query="Иван", record_types=types)

    def test_date_range_order_is_backend_responsibility(self):
        # The OpenAPI explicitly delegates this business validation to backend.
        ListEventsAction(date_from="2026-10-20", date_to_exclusive="2026-10-19")

    def test_status_intent_and_context_must_match(self):
        invalids = []
        for changes in [
            {"status": "error"},
            {"intent": "cancel_event"},
            {"action": {}},
            {"message": ""},
        ]:
            invalids.append({**CLARIFICATION, **changes})
        mismatch = deepcopy(CLARIFICATION)
        mismatch["intent"] = "list_events"
        invalids.append(mismatch)
        invalids.append(ready("list_events", {"query": "Иван"}, MESSAGES))
        ready_with_draft = ready("search_events", {"query": "Иван"}, MESSAGES)
        ready_with_draft["context"]["draft"] = DRAFT
        invalids.append(ready_with_draft)
        for value in invalids:
            with self.subTest(value=value), self.assertRaises(ValidationError):
                JOB_RESPONSE_ADAPTER.validate_python(value)

    def test_stored_context_accepts_all_draft_variants_including_ambiguous_empty(self):
        for draft in [{}, DRAFT, {"date_from": "2026-10-19"}, {"query": "Иван"}]:
            value = {**FIRST_REQUEST, "context": {"recent_messages": MESSAGES, "draft": draft}}
            parsed = JobRequest.model_validate(value)
            self.assertEqual(parsed.context.draft.model_dump(exclude_unset=True), draft)

    def test_problem_and_public_json_schema(self):
        value = {
            "type": "about:blank",
            "title": "Invalid request",
            "status": 400,
            "code": "bad_request",
        }
        self.assertEqual(Problem.model_validate(value).model_dump(exclude_unset=True), value)
        with self.assertRaises(ValidationError):
            Problem.model_validate({**value, "detail": None})
        schema = TypeAdapter(CreateEventDraft).json_schema()
        self.assertNotIn("default", schema["properties"]["title"])
        self.assertEqual(schema["properties"]["date"]["format"], "date")
        JOB_RESPONSE_ADAPTER.json_schema()


if __name__ == "__main__":
    unittest.main()
