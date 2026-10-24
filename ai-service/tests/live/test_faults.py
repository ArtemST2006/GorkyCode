import json
import time
from uuid import uuid4

from live_support import LiveCase, fault_server, job_payload


class FaultTests(LiveCase):
    def fault(self, overrides):
        if self.live.args.no_faults:
            self.skipTest("isolated real fault profiles disabled by --no-faults")
        return fault_server(self.live, overrides)

    def test_missing_cloudru_key_returns_503_and_health_survives(self):
        with self.fault({"CLOUDRU_API_KEY": "", "API_KEY": "", "EMBEDDING_ENABLED": "false"}) as base:
            self.assertEqual(self.live.call("/health", base_url=base, method="GET")["status"], 200)
            self.problem(self.live.call("/api/job", base_url=base, payload=job_payload()),
                         503, "model_not_configured")

    def test_disabled_embeddings_return_503_and_real_job_still_works(self):
        with self.fault({"EMBEDDING_ENABLED": "false"}) as base:
            self.problem(self.live.call("/api/embed", base_url=base,
                                       payload={"purpose": "query", "text": "Отчёт"}),
                         503, "embedding_unavailable")
            result = self.live.call("/api/job", base_url=base, paid=True, payload=job_payload())
            self.assertEqual(result["status"], 200)
            self.live.schema(result["body"], "JobResponse")

    def test_missing_weights_do_not_crash_process(self):
        with self.fault({"EMBEDDING_ENABLED": "true", "EMBEDDING_MODEL_PATH": "/nonexistent-ai-live-model"}) as base:
            self.assertEqual(self.live.call("/health", base_url=base, method="GET")["status"], 200)
            self.problem(self.live.call("/api/embed", base_url=base,
                                       payload={"purpose": "document", "text": "Отчёт"}),
                         503, "embedding_unavailable")

    def test_invalid_key_is_rejected_by_real_cloudru(self):
        with self.fault({"EMBEDDING_ENABLED": "false", "CLOUDRU_API_KEY": "invalid-ai-live-test-key"}) as base:
            response = self.live.call("/api/job", base_url=base, payload=job_payload(), paid=True,
                                      expected_technical=True)
            self.problem(response, 503, {"model_authentication_failed", "model_request_rejected"})
            self.assertFalse("invalid-ai-live-test-key" in json.dumps(response), "credential leaked in Problem")

    def test_real_process_logs_omit_user_text_and_unknown_paths(self):
        with self.fault({"EMBEDDING_ENABLED": "false", "LOG_LEVEL": "DEBUG"}) as base:
            marker = "PRIVATE_LIVE_MARKER_" + uuid4().hex
            self.problem(self.live.call("/api/job", base_url=base,
                                       payload={"text": marker}), 400, "invalid_request")
            self.problem(self.live.call("/" + marker, base_url=base, method="GET"), 404, "http_error")
            time.sleep(0.1)
            logs = self.live.fault_log_path.read_text()
            self.assertIn("route=unmatched", logs, "request logging is not configured for this runtime")
            self.assertFalse(marker in logs, "private request text/path leaked in server logs")

    def test_unknown_model_is_rejected_by_real_cloudru(self):
        with self.fault({"EMBEDDING_ENABLED": "false", "CLOUDRU_MODEL": "ai-live-test/nonexistent-model"}) as base:
            self.problem(self.live.call("/api/job", base_url=base, payload=job_payload(), paid=True,
                                       expected_technical=True),
                         503, {"model_not_available", "model_request_rejected"})

    def test_real_provider_timeout_returns_504(self):
        with self.fault({"EMBEDDING_ENABLED": "false", "CLOUDRU_TIMEOUT_SECONDS": "0.001",
                         "JOB_TIMEOUT_SECONDS": "5"}) as base:
            self.problem(self.live.call("/api/job", base_url=base, payload=job_payload(), paid=True,
                                       expected_technical=True), 504, "model_timeout")

    def test_job_deadline_returns_504(self):
        with self.fault({"EMBEDDING_ENABLED": "false", "JOB_TIMEOUT_SECONDS": "0.001"}) as base:
            self.problem(self.live.call("/api/job", base_url=base, payload=job_payload(), paid=True,
                                       expected_technical=True), 504, "job_timeout")

    def test_real_encoder_timeout_keeps_cpu_slot_and_recovers(self):
        with self.fault({"EMBEDDING_ENABLED": "true", "EMBEDDING_TIMEOUT_SECONDS": "0.01",
                         "EMBEDDING_MAX_WORKERS": "1"}) as base:
            payload = {"purpose": "query", "text": "Подготовить большой отчёт для команды " * 100}
            self.problem(self.live.call("/api/embed", base_url=base, payload=payload), 504, "embedding_timeout")
            immediate = self.live.call("/api/embed", base_url=base, payload=payload)
            # A real worker may finish before the next HTTP round trip. Both states are valid.
            if immediate["status"] == 503:
                self.problem(immediate, 503, "embedding_unavailable")
            else:
                self.problem(immediate, 504, "embedding_timeout")
            for _ in range(30):
                time.sleep(0.1)
                response = self.live.call("/api/embed", base_url=base, payload=payload)
                if response["status"] != 503:
                    break
            self.problem(response, 504, "embedding_timeout")
            self.assertEqual(self.live.call("/health", base_url=base, method="GET")["status"], 200)

    def test_truncated_real_completion_is_500_or_explicitly_unobserved(self):
        with self.fault({"EMBEDDING_ENABLED": "false", "CLOUDRU_MAX_TOKENS": "128"}) as base:
            payload = job_payload(text="Создай задачу с точным полным названием «" +
                                  "Подготовить подробный отчёт для команды и партнёров " * 30 +
                                  "» завтра в 18:00. Сохрани полное название без сокращений.")
            result = self.live.call("/api/job", base_url=base, payload=payload, paid=True,
                                    expected_technical=True)
            if result["status"] == 200:
                self.live.schema(result["body"], "JobResponse")
                self.skipTest("real provider fit a valid response into 128 tokens; invalid-output 500 not observed")
            self.problem(result, 500, "model_invalid_output")
