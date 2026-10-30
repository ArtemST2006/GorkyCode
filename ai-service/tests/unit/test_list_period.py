"""A saved period reply goes to the model with its structured dialogue state."""

import json
import unittest

from app.core.config import Settings
from app.core.errors import ServiceError
from app.llm.messages import build_messages
from app.schemas.job import JobRequest
from app.services.jobs import JobService


def request(text="до конца недели", *, context=None):
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
        "text": text, "request_time": "2026-10-29T12:00:00+03:00",
        "timezone": "Europe/Moscow", "context": context,
    })


class ListPeriodServiceTest(unittest.IsolatedAsyncioTestCase):
    async def test_orphan_period_requires_context_without_model_call(self):
        class Gateway:
            async def complete(self, messages):
                raise AssertionError("An orphan period must not call the model")

        service = JobService(Settings(_env_file=None), Gateway())
        with self.assertRaises(ServiceError) as raised:
            await service.process(request(context={}))
        self.assertEqual(raised.exception.code, "dialogue_context_required")

    async def test_other_orphan_fragments_require_context(self):
        class Gateway:
            async def complete(self, messages):
                raise AssertionError("An orphan fragment must not call the model")

        service = JobService(Settings(_env_file=None), Gateway())
        for fragment in ("В 15:00", "№2", "да", "30 октября", "все показанные"):
            with self.subTest(fragment=fragment), self.assertRaises(ServiceError) as raised:
                await service.process(request(fragment, context={}))
            self.assertEqual(raised.exception.code, "dialogue_context_required")

    async def test_period_and_saved_filters_go_to_model(self):
        class Gateway:
            messages = None

            async def complete(self, messages):
                self.messages = messages
                return json.dumps({
                    "status": "ready", "intent": "list_events",
                    "message": "Проверяю встречи до конца недели.",
                    "action": {"record_types": ["meeting"], "date_from": "2026-10-29",
                               "date_to_exclusive": "2026-11-02"},
                    "draft": None, "topic_changed": False,
                })

        gateway = Gateway()
        result = await JobService(Settings(_env_file=None), gateway).process(request())
        self.assertIsNotNone(gateway.messages)
        payload = json.loads(gateway.messages[-1]["content"])
        self.assertEqual(payload["user_text"], "до конца недели")
        self.assertEqual(payload["context"]["draft"]["record_types"], ["meeting"])
        self.assertEqual(result.action.record_types, ["meeting"])
        self.assertEqual(result.action.date_to_exclusive, "2026-11-02")

    async def test_new_command_with_period_goes_to_model(self):
        class Gateway:
            messages = None

            async def complete(self, messages):
                self.messages = messages
                return json.dumps({
                    "status": "ready", "intent": "list_events",
                    "message": "Проверяю задачи.",
                    "action": {"record_types": ["task"]},
                    "draft": None, "topic_changed": True,
                })

        gateway = Gateway()
        await JobService(Settings(_env_file=None), gateway).process(
            request("Покажи задачи до конца недели"))
        self.assertIn("Покажи задачи до конца недели", gateway.messages[-1]["content"])


class ListPeriodPayloadTest(unittest.TestCase):
    def test_model_payload_has_exclusive_week_anchor(self):
        payload = json.loads(build_messages(request(), {})[-1]["content"])
        self.assertEqual(payload["calendar_anchors"]["this_week_end_exclusive"], "2026-11-02")


if __name__ == "__main__":
    unittest.main()
