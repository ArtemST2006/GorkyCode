from copy import deepcopy
import json

from live_support import LiveCase, ROOT, cloud, job_payload, parallel_calls, session


class JobTests(LiveCase):
    def test_delete_candidates_then_selected_number(self):
        if self.live.args.llm_suite == "smoke":
            self.skipTest("not in --llm-suite smoke")
        payload = job_payload(text="Удали встречи с Иваном сегодня")
        first = self.live.call("/api/job", paid=True, payload=payload)
        self.assertEqual(first["status"], 200)
        self.assertEqual(first["headers"].get("x-task-intent"), "on_delete")
        self.live.schema(first["body"], "JobResponse")
        self.assertNotIn("intent", first["body"])
        self.assertEqual(first["body"]["status"], "ready")
        self.assertEqual(first["body"]["action"]["phase"], "find_candidates")

        action = first["body"]["action"]
        candidate_ids = ["2d7f8e0b-2fae-4b46-835a-675f748f3b11",
                         "ee79bdbf-f5a8-4204-a821-4a4ab21d14f6"]
        context = {
            "recent_messages": first["body"]["context"]["recent_messages"]
            + [{"role": "assistant", "text": "Нашёл две встречи. Какие удалить?"}],
            "draft": {key: value for key, value in action.items()
                      if key != "phase" and value is not None},
            "candidates": [
                {"number": index, "id": task_id, "record_type": "meeting",
                 "title": "Встреча с Иваном", "date": action.get("date_from"),
                 "time": "15:00", "status": "active"}
                for index, task_id in enumerate(candidate_ids, start=1)
            ],
        }
        second = self.live.call(
            "/api/job", paid=True,
            payload=payload | {"text": "Удали №2", "context": context},
        )
        self.assertEqual(second["status"], 200)
        self.assertEqual(second["headers"].get("x-task-intent"), "on_delete")
        self.live.schema(second["body"], "JobResponse")
        self.assertNotIn("intent", second["body"])
        self.assertEqual(second["body"]["action"],
                         {"phase": "delete_confirmed", "task_ids": [candidate_ids[1]]})

    def test_parallel_requests_keep_their_own_ids(self):
        if self.live.args.llm_suite == "smoke":
            self.skipTest("not in --llm-suite smoke")
        payloads = [job_payload(text="Создай задачу купить молоко завтра в 09:00"),
                    job_payload(text="Создай напоминание полить цветы завтра в 18:00")]
        results = parallel_calls(lambda index: self.live.call("/api/job", paid=True, payload=payloads[index]), 2)
        for payload, result in zip(payloads, results):
            self.assertEqual(result["status"], 200)
            self.live.schema(result["body"], "JobResponse")
            self.assertEqual(result["body"]["user_id"], payload["user_id"])
            self.assertEqual(result["body"]["task_id"], payload["task_id"])
            self.assertEqual(result["body"]["context"]["recent_messages"][0]["text"], payload["text"])

    def run_scenario(self, scenario):
        contexts = {}
        identity = job_payload()
        for expected in scenario["steps"]:
            parent = expected["context_from"]
            with self.subTest(step=expected["id"]):
                if parent and parent not in contexts:
                    self.skipTest("prerequisite live clarification failed")
                payload = identity | dict(text=expected["text"], request_time=expected["request_time"],
                                          timezone=expected["timezone"],
                                          context=deepcopy(contexts[parent]) if parent else {})
                result = self.live.call("/api/job", paid=True, payload=payload)
                self.live.report["llm_responses"].append(dict(case=scenario["id"], step=expected["id"],
                                                            request=payload, response=result))
                self.live.schema(result["body"], "Problem" if result["status"] != 200 else "JobResponse")
                try:
                    cloud.check_step(result, payload, expected)
                except cloud.CheckFailed as exc:
                    self.fail(str(exc))
                if expected["status"] == "needs_clarification" and not expected["code"]:
                    if expected["id"] == "ask_title":
                        self.assertNotIn("title", result["body"]["context"]["draft"])
                    if expected["id"] == "ask_period":
                        self.assertNotIn("date_from", result["body"]["context"]["draft"])
                        self.assertNotIn("date_to_exclusive", result["body"]["context"]["draft"])
                    contexts[expected["id"]] = deepcopy(result["body"]["context"])


def install_scenario(scenario):
    def test(self):
        if self.live.args.llm_suite == "smoke" and scenario["id"] != "create_task":
            self.skipTest("not in --llm-suite smoke")
        self.run_scenario(scenario)
    setattr(JobTests, "test_dialogue_" + scenario["id"], test)


extra_scenarios = [
    dict(id="create_missing_title", steps=[cloud.step(
        "ask_title", "Создай задачу завтра в 18:00", status="needs_clarification",
        draft={"record_type": "task", "date": "2026-10-19", "time": "18:00"})]),
    dict(id="create_missing_date", steps=[cloud.step(
        "ask_date", "Создай задачу отправить отчёт в 18:00", status="needs_clarification",
        draft={"record_type": "task", "time": "18:00"}, title_terms=("отчет",))]),
    dict(id="create_multiple_clarifications", steps=[
        cloud.step("ask_title", "Создай задачу", status="needs_clarification", draft={"record_type": "task"}),
        cloud.step("ask_date", "Купить молоко", status="needs_clarification", context_from="ask_title",
                   draft={"record_type": "task"}, title_terms=("молок",)),
        cloud.step("ask_time", "Завтра", status="needs_clarification", context_from="ask_date",
                   draft={"record_type": "task", "date": "2026-10-19"}, title_terms=("молок",)),
        cloud.step("ready", "В 18:00", context_from="ask_time",
                   action={"record_type": "task", "date": "2026-10-19", "time": "18:00"}, title_terms=("молок",)),
    ]),
    dict(id="list_clarification", steps=[
        cloud.step("ask_period", "Покажи мои встречи", intent="list_events", status="needs_clarification",
                   draft={"record_types": ["meeting"]}),
        cloud.step("ready", "На следующей неделе", intent="list_events", context_from="ask_period",
                   request_time="2026-10-22T12:00:00+03:00",
                   action={"date_from": "2026-10-26", "date_to_exclusive": "2026-11-02", "record_types": ["meeting"]}),
    ]),
    dict(id="new_topic_after_midnight", steps=[
        cloud.step("old_topic", "Назначь встречу с Иваном завтра", status="needs_clarification",
                   draft={"record_type": "meeting", "date": "2026-10-19"}),
        cloud.step("new_topic", "Создай задачу купить молоко завтра в 18:00", context_from="old_topic",
                   new_topic=True, request_time="2026-10-19T00:01:00+03:00",
                   action={"record_type": "task", "date": "2026-10-20", "time": "18:00"}, title_terms=("молок",)),
    ]),
    dict(id="year_boundary", steps=[cloud.step(
        "ready", "Создай напоминание полить цветы завтра в 09:00", request_time="2026-12-31T12:00:00+03:00",
        action={"record_type": "reminder", "date": "2027-01-01", "time": "09:00"}, title_terms=("цвет",))]),
    dict(id="leap_day", steps=[cloud.step(
        "ready", "Создай задачу отправить отчёт завтра в 18:00", request_time="2028-02-28T12:00:00+03:00",
        action={"record_type": "task", "date": "2028-02-29", "time": "18:00"}, title_terms=("отчет",))]),
    dict(id="unsupported_update", steps=[cloud.step("reject", "Перенеси встречу с Иваном на завтра", code="unsupported_intent")]),
]

for scenario in cloud.scenarios() + extra_scenarios:
    install_scenario(scenario)


class ExtractionTests(LiveCase):
    pass


def install_extraction(case, defaults):
    def test(self):
        if session().args.llm_suite != "full":
            self.skipTest("extraction corpus requires --llm-suite full")
        expected = case["expected"]
        # Never send reference completions, expected fields, tags, or evaluation instructions.
        payload = job_payload(text=case["text"], context=deepcopy(case.get("context", {})),
                              request_time=case.get("request_time", defaults["request_time"]),
                              timezone=case.get("timezone", defaults["timezone"]))
        result = self.live.call("/api/job", paid=True, payload=payload)
        self.live.report["llm_responses"].append(dict(case=case["id"], request=payload, response=result))
        self.assertEqual(result["status"], 200)
        self.live.schema(result["body"], "JobResponse")
        body = result["body"]
        self.assertEqual(body["status"], expected["status"])
        self.assertEqual(body["intent"], expected["intent"])
        self.assertEqual(body["user_id"], payload["user_id"])
        self.assertEqual(body["task_id"], payload["task_id"])
        fields = body["action"] if expected["status"] == "ready" else body["context"]["draft"]
        shape = cloud.step("extraction", case["text"], status=expected["status"], intent=expected["intent"],
                           action=expected.get("fields", {}), draft=expected.get("fields", {}),
                           new_topic=expected.get("topic_changed", False))
        try:
            cloud.check_step(result, payload, shape)
        except cloud.CheckFailed as exc:
            self.fail(str(exc))
        cloud.contains_fields(fields, expected.get("fields", {}))
        for field in expected.get("field_absent", []):
            self.assertNotIn(field, fields)
        query = cloud.normalized(fields.get("query", ""))
        for text in expected.get("query", {}).get("contains", []):
            self.assertIn(cloud.normalized(text), query)
        for text in expected.get("query", {}).get("forbidden", []):
            self.assertNotIn(cloud.normalized(text), query)
        history = [] if expected.get("topic_changed") else deepcopy(payload["context"].get("recent_messages", []))
        history.append(dict(role="user", text=payload["text"]))
        if body["status"] == "needs_clarification":
            history.append(dict(role="assistant", text=body["message"]))
            self.assertIn("?", body["message"], "question rubric: manual review may be needed")
        else:
            self.assertIsNone(body["context"]["draft"])
            self.assertTrue(body["message"].strip())
        self.assertEqual(body["context"]["recent_messages"], history)
    setattr(ExtractionTests, "test_extract_" + case["id"].replace("-", "_"), test)


dataset = json.loads((ROOT / "tests/fixtures/search_extraction_cases.json").read_text())
for extraction_case in dataset["cases"]:
    install_extraction(extraction_case, dataset["defaults"])
