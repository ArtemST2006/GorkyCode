"""Dialogue regression: a period reply must not become a new create command."""

import json
import unittest

from app.core.config import Settings
from app.core.errors import ServiceError
from app.llm.messages import build_messages
from app.schemas.job import JobRequest
from app.services.context import build_job_response
from app.services.jobs import JobService
from app.services.list_period import resolve_list_period_reply


def request(text="до конца недели", *, context=None, when="2026-10-29T12:00:00+03:00"):
    if context is None:
        context = {
            "recent_messages": [
                {"role": "user", "text": "покажи все встречи которые у меня сейчас созданы"},
                {"role": "assistant", "text": "За какой период показать встречи?"},
            ],
            "draft": {"record_types": ["meeting"]},
        }
    return JobRequest.model_validate({
        "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
        "task_id": "7b612951-57ae-4ac4-a93c-42fa9c9e8359",
        "text": text, "request_time": when, "timezone": "Europe/Moscow", "context": context,
    })


class ListPeriodTest(unittest.TestCase):
    def test_periods_preserve_meeting_filter_ids_and_history(self):
        cases = [
            ("до конца недели", "2026-10-29", "2026-11-02"),
            ("До конца этой недели, пожалуйста!", "2026-10-29", "2026-11-02"),
            ("на следующей неделе", "2026-11-02", "2026-11-09"),
            ("до конца следующей недели", "2026-10-29", "2026-11-09"),
            ("завтра", "2026-10-30", "2026-10-31"),
            ("послезавтра", "2026-10-31", "2026-11-01"),
            ("сегодня", "2026-10-29", "2026-10-30"),
            ("за эту неделю", "2026-10-26", "2026-11-02"),
            ("до конца месяца", "2026-10-29", "2026-11-01"),
            ("за этот месяц", "2026-10-01", "2026-11-01"),
            ("на 2026-11-05", "2026-11-05", "2026-11-06"),
        ]
        for text, start, end in cases:
            with self.subTest(text=text):
                req = request(text)
                decision = resolve_list_period_reply(req)
                self.assertIsNotNone(decision)
                response = build_job_response(req, decision)
                self.assertEqual(response.intent, "list_events")
                self.assertEqual(response.status, "ready")
                self.assertEqual(response.user_id, req.user_id)
                self.assertEqual(response.task_id, req.task_id)
                self.assertEqual(response.action.date_from, start)
                self.assertEqual(response.action.date_to_exclusive, end)
                self.assertEqual(response.action.record_types, ["meeting"])
                self.assertEqual(len(response.context.recent_messages), 3)
                self.assertEqual(response.context.recent_messages[-1].text, text)

    def test_timezone_and_calendar_boundaries(self):
        for when, text, start, end in [
            ("2026-11-01T22:30:00Z", "до конца недели", "2026-11-02", "2026-11-09"),
            ("2026-11-01T12:00:00+03:00", "до конца недели", "2026-11-01", "2026-11-02"),
            ("2026-12-31T12:00:00+03:00", "до конца месяца", "2026-12-31", "2027-01-01"),
            ("2028-02-28T12:00:00+03:00", "до конца месяца", "2028-02-28", "2028-03-01"),
        ]:
            with self.subTest(when=when):
                result = resolve_list_period_reply(request(text, when=when))
                self.assertEqual((result.action.date_from, result.action.date_to_exclusive),
                                 (start, end))

    def test_new_commands_negations_and_extra_filters_still_use_model(self):
        for text in [
            "Создай задачу купить билеты до конца недели", "Удали встречи до конца недели",
            "Перенеси на завтра", "Не до конца недели", "Завтра с Иваном", "Завтра в 15:00",
            "до конца недели, а потом удали их", "на 2026-02-30", "до конца недели?",
        ]:
            with self.subTest(text=text):
                self.assertIsNone(resolve_list_period_reply(request(text)))

    def test_orphan_reply_is_not_assigned_to_a_dialogue(self):
        self.assertIsNone(resolve_list_period_reply(request(context={})))

    def test_create_search_and_mutation_contexts_are_not_listed(self):
        contexts = [
            ("Создай встречу", "На какую дату назначить встречу?", {"record_type": "meeting"}),
            ("Найди встречу с Иваном", "За какой период показать встречи?", {"query": "с Иваном"}),
            ("Покажи встречи с Иваном", "За какой период показать встречи?", {}),
            ("Покажи только активные встречи", "За какой период показать встречи?", {}),
            ("Удали встречи", "За какой период показать встречи?", {"record_types": ["meeting"]}),
            ("Покажи встречи и удали их", "За какой период показать встречи?", {}),
            ("Покажи встречи", "Какие встречи показать?", {"record_types": ["meeting"]}),
            ("Покажи встречи", "За какой период показать встречи?", {"date_from": "2026-10-20"}),
        ]
        for user, question, draft in contexts:
            with self.subTest(user=user, draft=draft):
                req = request(context={"recent_messages": [
                    {"role": "user", "text": user}, {"role": "assistant", "text": question},
                ], "draft": draft})
                self.assertIsNone(resolve_list_period_reply(req))

    def test_empty_legacy_draft_recovers_explicit_type_from_question(self):
        values = request().model_dump(mode="json", exclude_unset=True)
        values["context"]["draft"] = {}
        result = resolve_list_period_reply(JobRequest.model_validate(values))
        self.assertEqual(result.action.record_types, ["meeting"])

    def test_generic_empty_legacy_draft_uses_model_to_recover_filters(self):
        values = request().model_dump(mode="json", exclude_unset=True)
        values["context"]["draft"] = {}
        values["context"]["recent_messages"][-1]["text"] = "За какой период показать события?"
        self.assertIsNone(resolve_list_period_reply(JobRequest.model_validate(values)))

    def test_multiple_types_are_preserved(self):
        values = request().model_dump(mode="json", exclude_unset=True)
        values["context"]["draft"]["record_types"] = ["task", "reminder"]
        result = resolve_list_period_reply(JobRequest.model_validate(values))
        self.assertEqual(result.action.record_types, ["task", "reminder"])

    def test_model_payload_has_exclusive_week_anchor(self):
        payload = json.loads(build_messages(request(), {})[-1]["content"])
        self.assertEqual(payload["calendar_anchors"]["this_week_end_exclusive"], "2026-11-02")


class ListPeriodServiceTest(unittest.IsolatedAsyncioTestCase):
    async def test_orphan_period_returns_client_error_without_paid_model_call(self):
        class Gateway:
            async def complete(self, messages):
                raise AssertionError("An orphan period must not call the model")

        service = JobService(Settings(_env_file=None), Gateway())
        with self.assertRaises(ServiceError) as raised:
            await service.process(request(context={}))
        self.assertEqual(raised.exception.status_code, 400)
        self.assertEqual(raised.exception.code, "dialogue_context_required")

    async def test_two_turn_dialogue_cannot_lose_intent_on_period_reply(self):
        class Gateway:
            calls = 0

            async def complete(self, messages):
                self.calls += 1
                if self.calls > 1:
                    raise AssertionError("An explicit period reply should not call the model")
                return json.dumps({
                    "status": "needs_clarification", "intent": "list_events",
                    "message": "За какой период показать встречи?", "action": None,
                    "draft": {"record_types": ["meeting"]}, "topic_changed": False,
                })

        gateway = Gateway()
        service = JobService(Settings(_env_file=None), gateway)
        first = await service.process(request("покажи все встречи которые у меня сейчас созданы",
                                              context={}))
        followup = request(context=first.context.model_dump(mode="json", exclude_unset=True))
        with self.assertLogs("ai_service", level="INFO") as logs:
            second = await service.process(followup)
        self.assertEqual(second.status, "ready")
        self.assertEqual(second.intent, "list_events")
        self.assertEqual(second.action.record_types, ["meeting"])
        self.assertEqual(second.task_id, first.task_id)
        self.assertEqual(gateway.calls, 1)
        self.assertIn("source=list_period_reply", " ".join(logs.output))
        self.assertNotIn(followup.text, " ".join(logs.output))


if __name__ == "__main__":
    unittest.main()
