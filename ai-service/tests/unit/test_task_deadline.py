"""Date-only task handling stays deterministic without a provider call."""

import unittest

from app.llm.schemas import ModelDecision
from app.schemas.job import JobRequest
from app.services.task_deadline import normalize_task_deadline


USER_ID = "b80e93e1-9945-48e0-8460-3ee68bcfb402"
TASK_ID = "7b612951-57ae-4ac4-a93c-42fa9c9e8359"


def request(text: str, context: dict | None = None) -> JobRequest:
    return JobRequest.model_validate({
        "user_id": USER_ID, "task_id": TASK_ID, "text": text,
        "request_time": "2026-10-26T12:00:00+03:00",
        "timezone": "Europe/Moscow", "context": context or {},
    })


def clarification(record_type: str = "task", date: str | None = "2026-11-01") -> ModelDecision:
    draft = {"record_type": record_type, "title": "Купить билеты"}
    if date is not None:
        draft["date"] = date
    return ModelDecision.model_validate({
        "status": "needs_clarification", "intent": "create_event",
        "message": "В какое время?", "action": None,
        "draft": draft, "topic_changed": False,
    })


class TaskDeadlineTest(unittest.TestCase):
    def test_end_of_week_creates_date_only_task(self):
        result = normalize_task_deadline(
            request("Поставь задачу купить билеты до конца недели"),
            clarification(date=None),
        )
        self.assertEqual(result.status, "ready")
        self.assertEqual(result.action.date, "2026-11-01")
        self.assertIsNone(result.action.time)
        self.assertIsNone(result.draft)
        self.assertNotIn("в какое время", result.message.casefold())

    def test_date_only_task_does_not_ask_time(self):
        result = normalize_task_deadline(
            request("Купить билеты завтра"), clarification()
        )
        self.assertEqual(result.status, "ready")
        self.assertIsNone(result.action.time)

    def test_meeting_still_needs_time(self):
        result = normalize_task_deadline(
            request("Встреча завтра"), clarification("meeting")
        )
        self.assertEqual(result.status, "needs_clarification")

    def test_explicit_time_is_preserved(self):
        decision = ModelDecision.model_validate({
            "status": "ready", "intent": "create_event", "message": "Задача создана.",
            "action": {
                "record_type": "task", "title": "Купить билеты",
                "date": "2026-11-01", "time": "15:00",
            },
            "draft": None, "topic_changed": False,
        })
        result = normalize_task_deadline(
            request("Купить билеты до конца недели в 15:00"), decision,
        )
        self.assertEqual(result.action.time, "15:00")

    def test_invented_deadline_time_is_removed_from_message_and_action(self):
        decision = ModelDecision.model_validate({
            "status": "ready", "intent": "create_event",
            "message": "Задача создана к воскресенью в 23:59.",
            "action": {
                "record_type": "task", "title": "Купить билеты",
                "date": "2026-11-01", "time": "23:59",
            },
            "draft": None, "topic_changed": False,
        })
        result = normalize_task_deadline(
            request("Купить билеты до конца недели"), decision
        )
        self.assertIsNone(result.action.time)
        self.assertNotIn("23:59", result.message)

    def test_compact_explicit_hour_survives_deadline_normalization(self):
        decision = ModelDecision.model_validate({
            "status": "ready", "intent": "create_event", "message": "Готово.",
            "action": {"record_type": "task", "title": "Отчёт",
                       "date": "2026-11-01", "time": "15:00"},
            "draft": None, "topic_changed": False,
        })
        for hour in ("в15", "в 15", "к15", "в15ч", "в15 часов"):
            with self.subTest(hour=hour):
                result = normalize_task_deadline(
                    request("Отчёт до конца недели " + hour), decision)
                self.assertEqual(result.action.time, "15:00")

    def test_compact_date_is_not_an_explicit_hour(self):
        decision = ModelDecision.model_validate({
            "status": "ready", "intent": "create_event", "message": "Готово.",
            "action": {"record_type": "task", "title": "Отчёт",
                       "date": "2026-11-01", "time": "23:59"},
            "draft": None, "topic_changed": False,
        })
        for date in ("в15.11", "в 15/11", "к15-11", "в15 ноября", "в15-го", "в25"):
            with self.subTest(date=date):
                result = normalize_task_deadline(
                    request("Отчёт до конца недели, обсуждали " + date), decision)
                self.assertIsNone(result.action.time)


if __name__ == "__main__":
    unittest.main()
