import http.client
import json
import math
import time
import unicodedata
from urllib.parse import urlsplit

from live_support import LiveCase, parallel_calls


class EmbeddingTests(LiveCase):
    def vector(self, purpose, text):
        return self.embedding(self.live.call("/api/embed", payload={"purpose": purpose, "text": text}))

    def test_both_purposes_dimensions_norm_and_immutable_identity(self):
        document = self.vector("document", "Подготовить презентацию для команды")
        query = self.vector("query", "Подготовить презентацию для команды")
        self.assertGreater(max(abs(a-b) for a, b in zip(document, query)), 1e-5,
                           "query/document prefixes did not change the real vectors")
        other = self.vector("document", "Полить цветы на балконе")
        self.assertGreater(max(abs(a-b) for a, b in zip(document, other)), 1e-4)
        cosine = math.fsum(a*b for a, b in zip(document, query))
        self.assertTrue(-1.001 <= cosine <= 1.001)

    def test_repeatability_trim_and_unicode_nfc(self):
        text = "Напомнить про ёлку и йогурт"
        canonical = self.vector("query", text)
        for variant in (text, " \n" + text + "\t ", unicodedata.normalize("NFD", text)):
            with self.subTest(variant=variant):
                actual = self.vector("query", variant)
                self.assertLess(max(abs(a-b) for a, b in zip(canonical, actual)), 1e-6)

    def test_long_text_right_truncation_is_deterministic(self):
        prefix = "подготовить отчёт " * 200
        first = self.vector("document", prefix + "уникальный хвост один")
        second = self.vector("document", prefix + "уникальный хвост два")
        self.assertLess(max(abs(a-b) for a, b in zip(first, second)), 1e-6)

    def test_invalid_json_and_utf8(self):
        for raw in (b'{"purpose":', b'\xff', b''):
            with self.subTest(raw=repr(raw)):
                self.problem(self.live.call("/api/embed", raw=raw,
                                           headers={"Content-Type": "application/json"}), 400,
                             {"invalid_request", "http_error"} if raw == b'\xff' else "invalid_request")

    def test_unsupported_and_missing_media_types(self):
        for content_type in (None, "text/plain", "application/jsonp", "application/problem+json"):
            with self.subTest(content_type=content_type):
                headers = {} if content_type is None else {"Content-Type": content_type}
                self.problem(self.live.call("/api/embed", raw=b'{}', headers=headers),
                             415, "unsupported_media_type")

    def test_media_type_case_and_charset(self):
        result = self.live.call("/api/embed", raw=b'{"purpose":"query","text":"hello"}',
                                headers={"Content-Type": "Application/JSON; charset=utf-8"})
        self.embedding(result)

    def test_exact_16kib_limit(self):
        base = b'{"purpose":"query","text":"x"}'
        for size, status in ((16384, 200), (16385, 413)):
            with self.subTest(bytes=size):
                result = self.live.call("/api/embed", raw=base + b' ' * (size - len(base)),
                                        headers={"Content-Type": "application/json"})
                if status == 200:
                    self.embedding(result)
                else:
                    self.problem(result, status, "payload_too_large")

    def test_limit_counts_utf8_bytes(self):
        raw = json.dumps({"purpose": "query", "text": "я" * 8500}, ensure_ascii=False).encode()
        self.assertGreater(len(raw), 16384)
        self.problem(self.live.call("/api/embed", raw=raw, headers={"Content-Type": "application/json"}),
                     413, "payload_too_large")

    def test_chunked_upload_limit(self):
        for size, status in ((16384, 200), (16385, 413)):
            with self.subTest(bytes=size):
                raw = b'{"purpose":"query","text":"x"}'
                raw += b' ' * (size - len(raw))
                chunks = (raw[offset:offset+1024] for offset in range(0, len(raw), 1024))
                result = self.live.call("/api/embed", raw=chunks, chunked=True,
                                        headers={"Content-Type": "application/json"})
                if status == 200:
                    self.embedding(result)
                else:
                    self.problem(result, status, "payload_too_large")

    def test_slow_incomplete_upload_gets_504(self):
        location = urlsplit(self.live.base_url)
        kind = http.client.HTTPSConnection if location.scheme == "https" else http.client.HTTPConnection
        timeout = self.live.configuration.get("embedding_timeout_seconds", 10)
        connection = kind(location.hostname, location.port, timeout=timeout + 10)
        try:
            connection.putrequest("POST", "/api/embed")
            connection.putheader("Content-Type", "application/json")
            connection.putheader("Transfer-Encoding", "chunked")
            connection.endheaders()
            piece = b'{"purpose":'
            connection.send(f"{len(piece):X}\r\n".encode() + piece + b'\r\n')
            response = connection.getresponse()
            result = dict(status=response.status, content_type=response.getheader("Content-Type").split(";")[0],
                          body=json.loads(response.read()))
            self.problem(result, 504, "embedding_timeout")
        finally:
            connection.close()

    def test_real_cpu_saturation_and_recovery(self):
        count = min(32, self.live.configuration.get("embedding_max_workers", 1) + 7)
        results = parallel_calls(lambda index: self.live.call("/api/embed", payload={
            "purpose": "query", "text": "Подготовить длинный отчёт для команды " * 100}), count)
        self.assertTrue(any(result["status"] == 200 for result in results))
        self.assertTrue(any(result["status"] == 503 for result in results), "no busy response observed")
        for result in results:
            if result["status"] == 200:
                self.embedding(result)
            else:
                self.problem(result, 503, "embedding_unavailable")
        time.sleep(0.1)
        self.vector("query", "После нагрузки сервис снова работает")
        self.assertEqual(self.live.call("/health", method="GET")["status"], 200)

    def test_client_disconnect_does_not_leak_encoder_capacity(self):
        location = urlsplit(self.live.base_url)
        kind = http.client.HTTPSConnection if location.scheme == "https" else http.client.HTTPConnection
        connection = kind(location.hostname, location.port, timeout=5)
        payload = json.dumps({"purpose": "query", "text": "Подготовить отчёт " * 200}).encode()
        try:
            connection.request("POST", "/api/embed", body=payload, headers={"Content-Type": "application/json"})
        finally:
            connection.close()  # Abandon a genuine request while CPU inference can still be running.
        for _ in range(30):
            result = self.live.call("/api/embed", payload={"purpose": "query", "text": "После отключения"})
            if result["status"] != 503:
                break
            time.sleep(0.1)
        self.embedding(result)


INVALID = {
    "missing_all": {}, "missing_text": {"purpose": "query"}, "missing_purpose": {"text": "x"},
    "unknown_purpose": {"purpose": "passage", "text": "x"},
    "boolean_purpose": {"purpose": True, "text": "x"},
    "empty_text": {"purpose": "query", "text": ""},
    "blank_text": {"purpose": "query", "text": " \n\t"},
    "numeric_text": {"purpose": "query", "text": 42},
    "boolean_text": {"purpose": "query", "text": True},
    "null_text": {"purpose": "query", "text": None},
    "array_text": {"purpose": "query", "text": ["x"]},
    "batch_input": {"purpose": "query", "texts": ["x"]},
    "custom_dimensions": {"purpose": "query", "text": "x", "dimensions": 384},
    "custom_model": {"purpose": "query", "text": "x", "model": "other"},
    "expected_version": {"purpose": "query", "text": "x", "expected_model_version": "other"},
}


def install_invalid(name, payload):
    def test(self):
        self.problem(self.live.call("/api/embed", payload=payload), 400, "invalid_request")
    setattr(EmbeddingTests, "test_rejects_" + name, test)


for case_name, case_payload in INVALID.items():
    install_invalid(case_name, case_payload)
