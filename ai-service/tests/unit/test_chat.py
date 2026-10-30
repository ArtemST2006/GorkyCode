"""Chat intent must be text-only and retain dialogue history."""

import unittest
from datetime import datetime, timezone

from pydantic import ValidationError

from app.llm.schemas import ModelDecision
from app.schemas.job import JobRequest, ReadyChat
from app.services.context import build_job_response


class ChatIntentTest(unittest.TestCase):
    def test_chat_message_and_saved_history_are_plain_text(self):
        request = JobRequest.model_validate({
            "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
            "task_id": "7b612951-57ae-4ac4-a93c-42fa9c9e8359",
            "text": "Привет!", "request_time": "2026-10-30T12:00:00+03:00",
            "timezone": "Europe/Moscow", "context": {},
        })
        decision = ModelDecision.model_validate({
            "status": "ready", "intent": "chat",
            "message": "**Привет!** 👋\nЧем помочь?", "action": None,
            "draft": None, "topic_changed": False,
        })
        response = build_job_response(request, decision)
        self.assertEqual(response.message, "Привет! Чем помочь?")
        self.assertEqual(response.context.recent_messages[-1].text, response.message)

    def test_response_keeps_history_and_has_no_action(self):
        request = JobRequest.model_validate({
            "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
            "task_id": "7b612951-57ae-4ac4-a93c-42fa9c9e8359",
            "text": "Привет!", "request_time": datetime.now(timezone.utc),
            "timezone": "Europe/Moscow", "context": {},
        })
        decision = ModelDecision.model_validate({
            "status": "ready", "intent": "chat", "message": "Привет!", "action": None,
            "draft": None, "topic_changed": False,
        })
        response = build_job_response(request, decision)
        self.assertIsInstance(response, ReadyChat)
        self.assertIsNone(response.action)
        self.assertEqual([m.role for m in response.context.recent_messages], ["user", "assistant"])

    def test_chat_rejects_action(self):
        with self.assertRaises(ValidationError):
            ModelDecision.model_validate({
                "status": "ready", "intent": "chat", "message": "Готово",
                "action": {"record_type": "task", "title": "Тест", "date": "2026-10-30"},
                "draft": None, "topic_changed": False,
            })

    def test_cancel_draft_discards_old_topic_without_action(self):
        request = JobRequest.model_validate({
            "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
            "task_id": "7b612951-57ae-4ac4-a93c-42fa9c9e8359",
            "text": "Отмени, ничего не создавай",
            "request_time": "2026-10-30T12:00:00+03:00", "timezone": "Europe/Moscow",
            "context": {"recent_messages": [
                {"role": "user", "text": "Назначь встречу с Иваном завтра"},
                {"role": "assistant", "text": "Во сколько?"},
            ], "draft": {"record_type": "meeting", "title": "Встреча с Иваном", "date": "2026-10-31"}},
        })
        decision = ModelDecision.model_validate({
            "status": "ready", "intent": "chat", "message": "Хорошо, не буду выполнять это действие.",
            "action": None, "draft": None, "topic_changed": True,
        })
        response = build_job_response(request, decision)
        self.assertIsNone(response.action)
        self.assertIsNone(response.context.draft)
        self.assertEqual([item.text for item in response.context.recent_messages], [
            "Отмени, ничего не создавай", "Хорошо, не буду выполнять это действие.",
        ])


if __name__ == "__main__":
    unittest.main()
