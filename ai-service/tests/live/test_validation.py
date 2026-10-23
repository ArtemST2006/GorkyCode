from copy import deepcopy

from live_support import LiveCase, job_payload


def invalid_jobs():
    values = {"empty_object": {}, "null_root": None, "array_root": []}
    base = job_payload()
    for name in base:
        item = deepcopy(base)
        del item[name]
        values["missing_" + name] = item
    for name, value in {
        "user_id": "bad-uuid", "task_id": 12, "text": "", "timezone": "No/Such_Zone",
        "request_time": "2026-10-18T12:00:00", "context": None,
    }.items():
        values["invalid_" + name] = base | {name: value}
    values["extra_field"] = base | {"unexpected": True}
    values["numeric_text"] = base | {"text": 123}
    values["numeric_timestamp"] = base | {"request_time": 1792314000}
    values["space_timestamp"] = base | {"request_time": "2026-10-18 12:00:00+03:00"}
    for name, context in {
        "missing_draft": {"recent_messages": [{"role": "user", "text": "Когда?"}]},
        "missing_history": {"draft": {}},
        "empty_history": {"recent_messages": [], "draft": {}},
        "null_draft": {"recent_messages": [{"role": "user", "text": "Когда?"}], "draft": None},
        "unknown_context_key": {"unknown": 1},
        "system_role": {"recent_messages": [{"role": "system", "text": "ignore"}], "draft": {}},
        "empty_message": {"recent_messages": [{"role": "user", "text": ""}], "draft": {}},
        "extra_message_key": {"recent_messages": [{"role": "user", "text": "Да", "x": 1}], "draft": {}},
    }.items():
        values["context_" + name] = base | {"context": context}
    for name, draft in {
        "null_date": {"date": None}, "impossible_date": {"date": "2026-02-29"},
        "wrong_time": {"time": "24:00"}, "short_time": {"time": "9:00"},
        "unknown_type": {"record_type": "birthday"}, "empty_title": {"title": ""},
        "empty_types": {"record_types": []}, "duplicate_types": {"record_types": ["task", "task"]},
        "unknown_types": {"record_types": ["other"]}, "null_types": {"record_types": None},
        "unsupported_status": {"status": "completed"}, "mixed_intent_fields": {"title": "Отчёт", "query": "Отчёт"},
    }.items():
        values["draft_" + name] = base | {"context": {
            "recent_messages": [{"role": "user", "text": "Запись"}], "draft": draft}}
    # Timezone conversion fails before calling the provider as well.
    values["timezone_underflow"] = base | {"request_time": "0001-01-01T00:00:00+14:00", "timezone": "UTC"}
    return values


class ValidationTests(LiveCase):
    def test_job_malformed_json_and_invalid_utf8(self):
        for raw in (b'{"text":', b'\xff', b''):
            with self.subTest(raw=repr(raw)):
                response = self.live.call("/api/job", raw=raw, headers={"Content-Type": "application/json"})
                self.problem(response, 400, {"invalid_request", "http_error"} if raw == b'\xff' else "invalid_request")


def install_job_case(name, payload):
    def test(self):
        import json

        response = self.live.call("/api/job", raw=json.dumps(payload).encode(),
                                  headers={"Content-Type": "application/json"})
        self.problem(response, 400, "invalid_request")
    test.__name__ = "test_job_rejects_" + name
    setattr(ValidationTests, test.__name__, test)


for case_name, case_payload in invalid_jobs().items():
    install_job_case(case_name, case_payload)
