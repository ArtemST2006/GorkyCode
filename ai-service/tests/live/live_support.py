"""Real HTTP and process support. Never replace the gateway or encoder."""

from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from datetime import date, datetime
from hashlib import sha256
import http.client
import importlib
import json
import math
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from urllib.parse import urlsplit
from uuid import uuid4

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
runtime = importlib.import_module("service_runtime")
cloud = importlib.import_module("test_cloudru_live")

CONTEXT = None


def validate_schema(value, schema, document, location="$"):
    """Validate the schema keywords actually used in the authoritative OAS 3.0 contract."""
    if "$ref" in schema:
        target = document
        for part in schema["$ref"].removeprefix("#/").split("/"):
            target = target[part.replace("~1", "/").replace("~0", "~")]
        return validate_schema(value, target, document, location)
    if value is None and schema.get("nullable"):
        if "enum" in schema:
            assert None in schema["enum"], f"{location}: null not in enum"
        return
    for branch in schema.get("allOf", []):
        validate_schema(value, branch, document, location)
    for keyword in ("oneOf", "anyOf"):
        if keyword in schema:
            matches = 0
            for branch in schema[keyword]:
                try:
                    validate_schema(value, branch, document, location)
                except (AssertionError, ValueError, TypeError):
                    continue
                matches += 1
            assert matches == 1 if keyword == "oneOf" else matches >= 1, (
                f"{location}: {keyword} matched {matches} branches")
    kind = schema.get("type")
    if kind:
        checks = {"object": lambda: isinstance(value, dict),
                  "array": lambda: isinstance(value, list),
                  "string": lambda: isinstance(value, str),
                  "integer": lambda: type(value) is int,
                  "number": lambda: type(value) in (int, float) and math.isfinite(value),
                  "boolean": lambda: type(value) is bool}
        assert kind in checks and checks[kind](), f"{location}: expected {kind}"
    if "enum" in schema:
        assert value in schema["enum"], f"{location}: invalid enum"
    if isinstance(value, dict):
        assert set(schema.get("required", [])) <= value.keys(), f"{location}: missing required fields"
        assert len(value) <= schema.get("maxProperties", math.inf), f"{location}: too many fields"
        if schema.get("additionalProperties") is False:
            assert set(value) <= schema.get("properties", {}).keys(), f"{location}: extra fields"
        for key, item in value.items():
            if key in schema.get("properties", {}):
                validate_schema(item, schema["properties"][key], document, location + "." + key)
    if isinstance(value, list):
        assert len(value) >= schema.get("minItems", 0), f"{location}: too few items"
        assert len(value) <= schema.get("maxItems", math.inf), f"{location}: too many items"
        if schema.get("uniqueItems"):
            assert len({json.dumps(item, sort_keys=True) for item in value}) == len(value), (
                f"{location}: duplicate items")
        for index, item in enumerate(value):
            validate_schema(item, schema.get("items", {}), document, f"{location}[{index}]")
    if isinstance(value, str):
        assert len(value) >= schema.get("minLength", 0), f"{location}: empty string"
        if "pattern" in schema:
            assert re.search(schema["pattern"], value), f"{location}: pattern mismatch"
        fmt = schema.get("format")
        if fmt == "date":
            assert re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}", value), f"{location}: date format"
            date.fromisoformat(value)
        elif fmt == "date-time":
            parsed = datetime.fromisoformat(value.replace("Z", "+00:00").replace("z", "+00:00"))
            assert parsed.tzinfo is not None, f"{location}: timezone missing"
        elif fmt == "uuid":
            assert re.fullmatch(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", value), (
                f"{location}: invalid UUID")


def request(base_url, path, *, method="POST", payload=None, raw=None, headers=None,
            chunked=False, timeout=180):
    address = urlsplit(base_url)
    connection_type = http.client.HTTPSConnection if address.scheme == "https" else http.client.HTTPConnection
    connection = connection_type(address.hostname, address.port, timeout=timeout)
    headers = dict(headers or {})
    if payload is not None:
        raw = json.dumps(payload, ensure_ascii=False, allow_nan=False).encode()
        headers.setdefault("Content-Type", "application/json")
    started = time.monotonic()
    try:
        connection.request(method, path, body=raw, headers=headers, encode_chunked=chunked)
        response = connection.getresponse()
        body = response.read(1024 * 1024 + 1)
        assert len(body) <= 1024 * 1024, "oversized HTTP response"
        result = dict(status=response.status,
                      content_type=response.getheader("Content-Type", "").split(";", 1)[0].lower(),
                      headers={k.lower(): v for k, v in response.getheaders()},
                      duration_seconds=round(time.monotonic() - started, 3))
        try:
            result["body"] = json.loads(body, parse_constant=cloud.invalid_json_constant)
        except (ValueError, UnicodeError):
            result["body"] = None
        return result
    except (OSError, http.client.HTTPException) as exc:
        raise AssertionError(f"HTTP transport failed: {type(exc).__name__}") from None
    finally:
        connection.close()


class Session:
    def __init__(self, args, report):
        self.args, self.report = args, report
        self.last_paid = None
        self.cloud_blocked = False
        self.observed = []
        self.lock = threading.RLock()
        self.cloud_attempts = 0
        self.flush = lambda: None
        self.config_report = {}
        cloud.configure_runtime(args, self.config_report)
        self.configuration = self.config_report["configuration"]
        self.base_url = args.base_url
        report["runtime_configuration"] = self.config_report
        code, contract = cloud.subprocess_json(cloud.runtime_command(args, "contract"), 15)
        assert code == 0 and "document" in contract, "runtime contract export failed; rebuild the image"
        self.contract = contract["document"]
        self.contract_hash = contract["sha256"]
        self.manifest = json.loads((ROOT / "app/embeddings/manifest.json").read_text())
        canonical = json.dumps(self.manifest, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
        self.model_version = "e5-sha256:" + sha256(canonical.encode()).hexdigest()

    def call(self, path, *, base_url=None, paid=False, expected_technical=False, **kwargs):
        if paid:
            if self.cloud_blocked and not expected_technical:
                raise unittest.SkipTest("remaining Cloud.ru calls stopped after a technical failure")
            cloud.wait_interval(self.last_paid, self.args.interval)
            with self.lock:
                if self.cloud_attempts >= self.args.max_cloud_requests:
                    raise unittest.SkipTest("--max-cloud-requests reached")
                self.cloud_attempts += 1
        entry = dict(path=path, paid=paid, outcome="running")
        if path == "/api/job":
            entry["request"] = kwargs.get("payload")
        with self.lock:
            self.observed.append(entry)
        self.flush()
        try:
            result = request(base_url or self.base_url, path, timeout=self.args.timeout, **kwargs)
        except AssertionError:
            if paid:
                self.cloud_blocked = True
            with self.lock:
                entry["outcome"] = "transport_error"
            raise
        finally:
            if paid:
                self.last_paid = time.monotonic()
        code = result["body"].get("code") if isinstance(result["body"], dict) else None
        if paid and result["status"] >= 500 and not expected_technical and code != "model_invalid_output":
            self.cloud_blocked = True
        with self.lock:
            entry.update(status=result["status"], duration_seconds=result["duration_seconds"], outcome="received")
            if path == "/api/job" or result["status"] >= 400:
                entry["response"] = result["body"]
        self.flush()
        return result

    def schema(self, value, name):
        validate_schema(value, self.contract["components"]["schemas"][name], self.contract)


def session():
    if CONTEXT is None:
        raise unittest.SkipTest("use scripts/test_service_live.py with a running host/Docker service")
    return CONTEXT


def job_payload(**changes):
    return dict(user_id=str(uuid4()).upper(), task_id=str(uuid4()),
                text="Создай задачу отправить отчёт сегодня в 18:00",
                request_time=cloud.BASE_TIME, timezone="Europe/Moscow", context={}) | changes


class LiveCase(unittest.TestCase):
    def setUp(self):
        self.live = session()

    def problem(self, result, status, code=None):
        self.assertEqual(result["status"], status)
        cloud.check_problem(result)
        self.live.schema(result["body"], "Problem")
        if code is not None:
            self.assertIn(result["body"]["code"], {code} if isinstance(code, str) else code)

    def embedding(self, result):
        self.assertEqual(result["status"], 200)
        self.assertEqual(result["content_type"], "application/json")
        self.assertEqual(result["headers"].get("cache-control"), "no-store")
        self.live.schema(result["body"], "EmbeddingResponse")
        body = result["body"]
        self.assertEqual(set(body), {"embedding", "model", "model_version"})
        self.assertEqual(body["model"], self.live.manifest["model"])
        self.assertEqual(body["model_version"], self.live.model_version)
        vector = body["embedding"]
        self.assertEqual(len(vector), 384)
        self.assertTrue(all(type(x) in (int, float) and math.isfinite(x) for x in vector))
        self.assertAlmostEqual(math.sqrt(math.fsum(x*x for x in vector)), 1.0, delta=1e-3)
        return vector


def profile_environment(live):
    if live.args.runtime == "docker":
        code = "import json,os; keys=" + repr(sorted(runtime.SERVICE_KEYS)) + "; print(json.dumps({k:os.environ[k] for k in keys if k in os.environ}))"
        result = subprocess.run(["docker", "exec", live.args.container, "python", "-c", code],
                                capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, "cannot read selected container environment"
        selected = json.loads(result.stdout)  # Secrets stay only in memory; never add this to reports.
    elif live.args.host_state:
        before = dict(os.environ)
        try:
            runtime.load_process_environment(live.args.host_state["pid"], live.args.host_state["launch_id"])
            selected = {k: v for k, v in os.environ.items() if k in runtime.SERVICE_KEYS}
        finally:
            os.environ.clear()
            os.environ.update(before)
    else:
        environment = runtime.runtime_environment(live.args.env_file)
        selected = {k: v for k, v in environment.items() if k in runtime.SERVICE_KEYS}
    environment = dict(os.environ)
    for key in list(environment):
        if key.upper() in runtime.SERVICE_KEYS:
            del environment[key]
    environment.update(selected)
    environment.update(PYTHONDONTWRITEBYTECODE="1", PYTHONUNBUFFERED="1", CLOUDRU_MAX_RETRIES="0")
    return environment


@contextmanager
def fault_server(live, overrides):
    """Start an isolated real service, change config, then stop only our own resources."""
    environment = profile_environment(live)
    environment.update(overrides)
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 0))
        port = reservation.getsockname()[1]
    token = uuid4().hex
    name = "ai-live-test-" + token[:12]
    if live.args.runtime == "host":
        python = live.args.host_python or Path((live.args.host_state or {}).get("python", ROOT / ".venv/bin/python"))
        environment["PORT"] = str(port)
        command = [str(python), str(ROOT / "scripts/service_runtime.py"), "serve", "--env-file", "/dev/null"]
    else:
        environment["PORT"] = "8090"
        command = ["docker", "run", "--rm", "--name", name, "--label", "ai.live-test=" + token,
                   "--publish", f"127.0.0.1:{port}:8090"]
        for key in sorted(runtime.SERVICE_KEYS & environment.keys()):
            command += ["--env", key]
        command += [live.args.image]
    base_url = f"http://127.0.0.1:{port}"
    with tempfile.TemporaryDirectory(prefix="ai-live-test-") as directory:
        with (Path(directory) / "server.log").open("w+") as logs:
            process = subprocess.Popen(command, cwd=ROOT, env=environment, stdout=logs,
                                       stderr=subprocess.STDOUT, start_new_session=True)
            previous_log = getattr(live, "fault_log_path", None)
            live.fault_log_path = Path(directory) / "server.log"
            try:
                runtime.wait_until_ready(base_url, process, 120)
                yield base_url
            finally:
                if live.args.runtime == "docker":
                    labels = runtime.inspect_container(name)
                    if labels and labels.get("ai.live-test") == token:
                        subprocess.run(["docker", "stop", "--time", "10", name],
                                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                runtime.terminate_process(process)
                if live.args.runtime == "docker":
                    for _ in range(20):
                        if runtime.inspect_container(name) is None:
                            break
                        time.sleep(0.1)
                    assert runtime.inspect_container(name) is None, "temporary test container was not removed"
                live.fault_log_path = previous_log


def parallel_calls(function, count):
    import threading

    barrier = threading.Barrier(count)

    def run(index):
        barrier.wait(timeout=10)
        return function(index)

    with ThreadPoolExecutor(max_workers=count) as workers:
        return list(workers.map(run, range(count)))
