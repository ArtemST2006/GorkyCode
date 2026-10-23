from hashlib import sha256

from live_support import LiveCase, ROOT


class ContractTests(LiveCase):
    def test_health_exact_contract(self):
        response = self.live.call("/health", method="GET")
        self.assertEqual(response["status"], 200)
        self.assertEqual(response["content_type"], "application/json")
        self.assertEqual(response["body"], {"service": "ai-service", "status": "ok"})
        self.live.schema(response["body"], "AIHealthResponse")

    def test_published_openapi_equals_authoritative_contract(self):
        original = (ROOT.parent / "contracts/ai.openapi.yaml").read_bytes()
        for mirror in ("contracts/ai.openapi.yaml", "app/resources/ai.openapi.yaml"):
            self.assertEqual((ROOT / mirror).read_bytes(), original, mirror)
        self.assertEqual(self.live.contract_hash, sha256(original).hexdigest(), "deployed contract is stale")
        result = self.live.call("/openapi.json", method="GET")
        self.assertEqual(result["status"], 200)
        self.assertEqual(result["body"], self.live.contract)
        self.assertEqual(set(result["body"]["paths"]), {"/health", "/api/job", "/api/embed"})

    def test_documentation_routes(self):
        for path in ("/docs", "/redoc"):
            with self.subTest(path=path):
                result = self.live.call(path, method="GET")
                self.assertEqual(result["status"], 200)
                self.assertEqual(result["content_type"], "text/html")

    def test_unknown_path_is_problem(self):
        result = self.live.call("/no-such-ai-route", method="GET")
        self.problem(result, 404, "http_error")

    def test_wrong_methods_are_problems(self):
        for path, method in (("/health", "POST"), ("/api/job", "GET"), ("/api/embed", "DELETE")):
            with self.subTest(path=path, method=method):
                result = self.live.call(path, method=method)
                self.problem(result, 405, "http_error")
                self.assertIn("allow", result["headers"])
