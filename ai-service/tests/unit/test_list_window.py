"""A relative list window is extracted by the model into structured fields."""

import json
import unittest

from pydantic import ValidationError

from app.core.config import Settings
from app.schemas.job import JobRequest
from app.schemas.list_events import ListEventsAction
from app.services.jobs import JobService


class WindowGateway:
    def __init__(self):
        self.messages = None

    async def complete(self, messages):
        self.messages = messages
        return json.dumps({
            "status": "ready", "intent": "list_events",
            "message": "Показываю дела на ближайший час.",
            "action": {"upcoming_minutes": 60, "statuses": ["active"], "date_from": None},
            "draft": None, "topic_changed": False,
        })


class ListWindowTest(unittest.IsolatedAsyncioTestCase):
    async def test_text_and_schema_reach_model_and_null_filter_is_omitted(self):
        request = JobRequest.model_validate({
            "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
            "task_id": "7b612951-57ae-4ac4-a93c-42fa9c9e8359",
            "text": "а напомни что я должен сделать в течении часа?",
            "request_time": "2026-10-30T23:30:00+03:00",
            "timezone": "Europe/Moscow", "context": {},
        })
        gateway = WindowGateway()
        result = await JobService(Settings(_env_file=None), gateway).process(request)
        self.assertIsNotNone(gateway.messages)
        self.assertIn(request.text, gateway.messages[-1]["content"])
        self.assertIn("upcoming_minutes", gateway.messages[0]["content"])
        self.assertEqual(result.intent, "list_events")
        self.assertEqual(result.action.upcoming_minutes, 60)
        self.assertEqual(result.action.statuses, ["active"])
        self.assertNotIn("date_from", result.action.model_dump(exclude_unset=True))

    def test_window_bounds_are_validated(self):
        for minutes in (0, -1, 10081):
            with self.subTest(minutes=minutes), self.assertRaises(ValidationError):
                ListEventsAction.model_validate({"upcoming_minutes": minutes})


if __name__ == "__main__":
    unittest.main()
