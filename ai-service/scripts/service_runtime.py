#!/usr/bin/env python3
"""Host/Docker launchers and safe runtime inspection for real Cloud.ru tests."""

from __future__ import annotations

import argparse
import asyncio
import json
import math
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import subprocess
import sys
import time
from urllib.request import ProxyHandler, build_opener
from uuid import uuid4


ROOT = Path(__file__).resolve().parents[1]
STATE_DIR = ROOT / ".runtime"
INDEX = "https://pypi-mirror.gitverse.ru/simple/"
SERVICE_KEYS = {
    "LLM_PROVIDER", "API_KEY", "CLOUDRU_API_KEY", "CLOUDRU_BASE_URL", "CLOUDRU_MODEL",
    "CLOUDRU_FORCE_IPV4", "CLOUDRU_TIMEOUT_SECONDS", "CLOUDRU_MAX_RETRIES",
    "CLOUDRU_MAX_TOKENS", "CLOUDRU_MAX_COMPLETION_TOKENS", "JOB_TIMEOUT_SECONDS",
    "EMBEDDING_ENABLED", "EMBEDDING_MODEL_PATH", "EMBEDDING_NUM_THREADS",
    "EMBEDDING_MAX_WORKERS", "EMBEDDING_TIMEOUT_SECONDS", "LOG_LEVEL", "PORT",
    "SECURITY_GUARD_MODE", "SECURITY_GUARD_MODEL", "SECURITY_GUARD_TIMEOUT_SECONDS",
    "SECURITY_EXPOSE_DOCS",
    "SECURITY_GUARD_MAX_TOKENS", "SECURITY_GUARD_MAX_CONCURRENCY",
    "SECURITY_JOB_MAX_BODY_BYTES", "SECURITY_RESPOND_MAX_BODY_BYTES",
    "SECURITY_USER_MAX_CHARS", "SECURITY_HISTORY_MESSAGE_MAX_CHARS", "SECURITY_CONTEXT_MAX_CHARS",
}


class RuntimeFailure(Exception):
    """A safe message without configuration values or provider error bodies."""


def require(condition, message):
    if not condition:
        raise RuntimeFailure(message)


def port_number(value):
    number = int(value)
    if not 1 <= number <= 65535:
        raise argparse.ArgumentTypeError("port must be in 1..65535")
    return number


def positive_seconds(value):
    number = float(value)
    if not math.isfinite(number) or number <= 0:
        raise argparse.ArgumentTypeError("expected a finite positive duration")
    return number


def container_name(value):
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]*", value):
        raise argparse.ArgumentTypeError("invalid container name")
    return value


def runtime_environment(path):
    """Literal dotenv only. Never source shell code or pass secret values in argv."""
    values = {}
    if path.is_file():
        for line in path.read_text(encoding="utf-8-sig").splitlines():
            line = line.strip()
            if line.startswith("export "):
                line = line[7:].lstrip()
            key, sep, value = line.partition("=")
            key = key.strip().upper()
            if not sep or key not in SERVICE_KEYS:
                continue
            value = value.strip()
            if value.startswith(("'", '"')):
                end = value.find(value[0], 1)
                require(end >= 1 and (not value[end + 1:].strip()
                                     or value[end + 1:].lstrip().startswith("#")),
                        f"invalid quoted dotenv setting: {key}")
                value = value[1:end]
            else:
                value = re.split(r"\s+#", value, maxsplit=1)[0].rstrip()
            require("${" not in value, f"dotenv interpolation is unsupported: {key}")
            values[key] = value
    else:
        require(path == ROOT / ".env" or path == Path("/dev/null"), "env file does not exist")
    environment = dict(os.environ)
    process_values = {key.upper(): value for key, value in os.environ.items()
                      if key.upper() in SERVICE_KEYS}
    # Remove casing aliases so Pydantic and Docker see identical precedence.
    for key in list(environment):
        if key.upper() in SERVICE_KEYS:
            del environment[key]
    environment.update(values)
    environment.update(process_values)
    environment["PYTHONDONTWRITEBYTECODE"] = "1"
    environment["PYTHONUNBUFFERED"] = "1"
    return environment


def settings_from_environment():
    sys.path.insert(0, str(ROOT))
    from app.core.config import Settings

    return Settings(_env_file=None)


def canonical_environment(settings):
    environment = {key.upper(): str(value).lower() if isinstance(value, bool) else str(value)
                   for key, value in settings.model_dump().items()
                   if key != "cloudru_api_key"}
    environment["CLOUDRU_API_KEY"] = (
        settings.cloudru_api_key.get_secret_value() if settings.cloudru_api_key else "")
    return environment


def public_configuration(settings):
    return dict(provider=settings.llm_provider, model=settings.cloudru_model,
                base_url=settings.cloudru_base_url, port=settings.port,
                key_configured=bool(settings.cloudru_api_key
                                    and settings.cloudru_api_key.get_secret_value().strip()),
                provider_timeout_seconds=settings.cloudru_timeout_seconds,
                job_timeout_seconds=settings.job_timeout_seconds,
                max_tokens=settings.cloudru_max_tokens,
                provider_max_retries=settings.cloudru_max_retries,
                force_ipv4=settings.cloudru_force_ipv4,
                embedding_enabled=settings.embedding_enabled,
                embedding_max_workers=settings.embedding_max_workers,
                embedding_timeout_seconds=settings.embedding_timeout_seconds,
                embedding_model_path=settings.embedding_model_path)


def load_process_environment(pid, launch_id):
    require(pid > 1 and bool(re.fullmatch(r"[0-9a-f]{32}", launch_id or "")),
            "invalid host runtime identity")
    try:
        pairs = Path(f"/proc/{pid}/environ").read_bytes().split(b"\0")
    except OSError:
        raise RuntimeFailure("host service process is no longer available") from None
    selected = {}
    identity = None
    for pair in pairs:
        key, sep, value = pair.partition(b"=")
        if sep and key == b"AI_SERVICE_LAUNCH_ID":
            identity = value.decode()
        elif sep and key.decode(errors="replace").upper() in SERVICE_KEYS:
            selected[key.decode().upper()] = value.decode()
    require(identity == launch_id, "host process does not belong to this launcher")
    for key in list(os.environ):
        if key.upper() in SERVICE_KEYS:
            del os.environ[key]
    os.environ.update(selected)


def info_in_runtime(args):
    if args.command == "contract":
        from hashlib import sha256
        from importlib.resources import files

        sys.path.insert(0, str(ROOT))
        from app.api.openapi import load_contract

        raw = files("app").joinpath("resources/ai.openapi.yaml").read_bytes()
        print(json.dumps({"document": load_contract(), "sha256": sha256(raw).hexdigest()}))
        return 0
    if args.pid:
        load_process_environment(args.pid, args.launch_id)
    else:
        environment = runtime_environment(args.env_file)
        os.environ.clear()
        os.environ.update(environment)
    settings = settings_from_environment()
    if args.command == "info":
        print(json.dumps(public_configuration(settings)), flush=True)
        return 0
    configuration = public_configuration(settings)
    result = dict(configuration=configuration, passed=False, requests_sent=0)
    if not configuration["key_configured"]:
        result["error"] = "model_not_configured"
        print(json.dumps(result), flush=True)
        return 1
    from urllib.parse import urlsplit

    require(urlsplit(settings.cloudru_base_url).hostname == "foundation-models.api.cloud.ru",
            "expected the official Cloud.ru endpoint")
    from app.core.errors import ServiceError
    from app.core.logging import configure_logging
    from app.llm.cloudru import CloudRuGateway

    configure_logging(settings.log_level)

    async def probe():
        # The live probe has one attempt; service retry settings are reported separately.
        probe_settings = settings.model_copy(update={
            "cloudru_max_retries": 0, "cloudru_timeout_seconds": args.timeout,
            "cloudru_max_tokens": args.max_tokens,
        })
        gateway = CloudRuGateway(probe_settings)
        started = time.monotonic()
        try:
            result["requests_sent"] = 1
            async with asyncio.timeout(args.timeout + 2):
                await gateway.complete([{"role": "user", "content": "Ответь одним словом: готово."}])
            result.update(passed=True, status=200, finish_reason="stop")
        except ServiceError as exc:
            result.update(error=exc.code, status=exc.status_code)
        except Exception as exc:
            result["error"] = type(exc).__name__
        finally:
            result["duration_seconds"] = round(time.monotonic() - started, 3)
            await gateway.close()

    asyncio.run(probe())
    print(json.dumps(result), flush=True)
    return 0 if result["passed"] else 1


def captured_json(command, *, environment=None):
    result = subprocess.run(command, cwd=ROOT, env=environment, text=True, capture_output=True)
    require(result.returncode == 0, "runtime configuration failed; check dependencies and dotenv settings")
    try:
        return json.loads(result.stdout)
    except ValueError:
        raise RuntimeFailure("runtime returned invalid configuration JSON") from None


def write_state(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp-" + value["launch_id"])
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(path)


def clear_state(path, identity):
    try:
        if json.loads(path.read_text()).get("launch_id") == identity:
            path.unlink()
    except (OSError, ValueError):
        pass


def check_port_available(port):
    try:
        with socket.socket() as connection:
            connection.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            connection.bind(("127.0.0.1", port))
    except OSError:
        raise RuntimeFailure(f"port {port} is unavailable; choose --port") from None


def wait_until_ready(base_url, process, timeout):
    opener = build_opener(ProxyHandler({}))
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        require(process.poll() is None, "service exited before readiness; see its logs above")
        try:
            with opener.open(base_url + "/health", timeout=1) as response:
                body = json.loads(response.read(4096))
                if response.status == 200 and body == {"service": "ai-service", "status": "ok"}:
                    return
        except (OSError, ValueError):
            pass
        time.sleep(0.25)
    raise RuntimeFailure("service readiness timeout; see its logs above")


def terminate_process(process):
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
        process.wait(timeout=15)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(timeout=5)
    except ProcessLookupError:
        pass


def validate_cloudru(environment, allow_no_key):
    require(environment.get("LLM_PROVIDER", "cloudru") == "cloudru", "LLM_PROVIDER must be cloudru")
    key = environment.get("CLOUDRU_API_KEY", environment.get("API_KEY", "")).strip()
    require(bool(key) or allow_no_key, "CLOUDRU_API_KEY/API_KEY is missing; configure .env")


def host(args):
    environment = runtime_environment(args.env_file)
    if args.port is not None:
        environment["PORT"] = str(args.port)
    if args.disable_embeddings:
        environment["EMBEDDING_ENABLED"] = "false"
    validate_cloudru(environment, args.allow_no_key)
    selected_port = port_number(environment.get("PORT", "8090"))
    check_port_available(selected_port)
    python = args.venv.resolve() / "bin/python"
    if not python.is_file():
        require(not args.no_install, "venv missing; run without --no-install first")
        print(f"CREATE VENV {args.venv}", flush=True)
        subprocess.run([args.python, "-m", "venv", str(args.venv)], check=True, cwd=ROOT)
    version = subprocess.run([str(python), "-c", "import sys; print('.'.join(map(str,sys.version_info[:2])))"],
                             check=True, capture_output=True, text=True).stdout.strip()
    require(version == "3.12", "the pinned runtime requires Python 3.12; choose --python/--venv")
    if not args.no_install:
        print("INSTALL pinned service dependencies", flush=True)
        installed_torch = subprocess.run([
            str(python), "-c", "from importlib.metadata import version; print(version('torch'))"],
            capture_output=True, text=True)
        if installed_torch.returncode != 0 or installed_torch.stdout.strip() != "2.10.0+cpu":
            subprocess.run([str(python), "-m", "pip", "install", "--no-deps",
                            "--index-url", args.torch_index_url, "torch==2.10.0+cpu"],
                           check=True, cwd=ROOT, env=environment)
        subprocess.run([str(python), "-m", "pip", "install", "--index-url", args.index_url,
                        "-c", str(ROOT / "requirements.lock"), "--editable", str(ROOT)],
                       check=True, cwd=ROOT, env=environment)
        subprocess.run([str(python), "-m", "pip", "check"], check=True, cwd=ROOT, env=environment)
    configuration = captured_json([str(python), str(Path(__file__).resolve()), "info",
                                   "--env-file", "/dev/null"], environment=environment)
    if configuration["embedding_enabled"]:
        model_path = Path(configuration["embedding_model_path"])
        if not model_path.is_absolute():
            model_path = ROOT / model_path
        environment["EMBEDDING_MODEL_PATH"] = str(model_path)
        configuration["embedding_model_path"] = str(model_path)
        command = [str(python), str(ROOT / "scripts/download_embedding_model.py"), str(model_path)]
        if args.no_download:
            command.append("--check-only")
        subprocess.run(command, check=True, cwd=ROOT, env=environment)
    identity = uuid4().hex
    environment["AI_SERVICE_LAUNCH_ID"] = identity
    base_url = f"http://127.0.0.1:{selected_port}"
    state_path = STATE_DIR / "host.json"
    process = subprocess.Popen([str(python), str(Path(__file__).resolve()), "serve",
                                "--env-file", "/dev/null"], cwd=ROOT, env=environment,
                               start_new_session=True)
    try:
        wait_until_ready(base_url, process, args.startup_timeout)
        write_state(state_path, dict(runtime="host", launch_id=identity, pid=process.pid,
                                     python=str(python), base_url=base_url,
                                     configuration=configuration))
        print(f"READY host {base_url} | Ctrl+C stops this process", flush=True)
        return process.wait()
    finally:
        terminate_process(process)
        clear_state(state_path, identity)


def serve(args):
    os.environ.update(runtime_environment(args.env_file))
    settings = settings_from_environment()
    os.environ.update(canonical_environment(settings))
    os.environ["HF_HUB_OFFLINE"] = "1"
    os.environ["TRANSFORMERS_OFFLINE"] = "1"
    os.environ["TOKENIZERS_PARALLELISM"] = "false"
    # exec preserves PID and puts the exact effective config in /proc/PID/environ.
    bootstrap = (
        "import logging,os,uvicorn; "
        "level=os.environ['LOG_LEVEL']; "
        "logging.basicConfig(level=level,format='%(levelname)s %(name)s %(message)s'); "
        "uvicorn.run('app.main:app',host='127.0.0.1',port=int(os.environ['PORT']),"
        "log_level=level.lower(),access_log=False,server_header=False)"
    )
    os.execvpe(sys.executable, [sys.executable, "-c", bootstrap], os.environ)


def inspect_container(name):
    result = subprocess.run(["docker", "container", "inspect", "--format",
                             '{{json .Config.Labels}}', name], capture_output=True, text=True)
    if result.returncode != 0:
        return None
    return json.loads(result.stdout) or {}


def docker(args):
    require(shutil.which("docker"), "docker command is missing")
    subprocess.run(["docker", "info", "--format", "{{.ServerVersion}}"], check=True)
    require(inspect_container(args.name) is None,
            "container already exists; stop it yourself or choose --name")
    check_port_available(args.port)
    environment = runtime_environment(args.env_file)
    validate_cloudru(environment, args.allow_no_key)
    if args.disable_embeddings:
        environment["EMBEDDING_ENABLED"] = "false"
    if not args.no_build:
        print(f"BUILD {args.image}", flush=True)
        subprocess.run(["docker", "build", "--progress=plain", "--build-arg",
                        "PIP_INDEX_URL=" + args.index_url, "--build-arg",
                        "TORCH_INDEX_URL=" + args.torch_index_url,
                        "--tag", args.image, str(ROOT)], check=True)
    environment["PORT"] = "8090"
    environment["EMBEDDING_MODEL_PATH"] = "/opt/embedding-model"
    identity = uuid4().hex
    label = "gorkycode.ai.launch-id"
    command = ["docker", "run", "--rm", "--name", args.name,
               "--label", label + "=" + identity,
               "--publish", f"127.0.0.1:{args.port}:8090"]
    for key in sorted(SERVICE_KEYS & environment.keys()):
        command += ["--env", key]  # Docker reads the value from its process environment.
    command.append(args.image)
    base_url = f"http://127.0.0.1:{args.port}"
    state_path = STATE_DIR / ("docker-" + args.name + ".json")
    process = subprocess.Popen(command, env=environment, cwd=ROOT, start_new_session=True)
    try:
        wait_until_ready(base_url, process, args.startup_timeout)
        configuration = captured_json(["docker", "exec", args.name, "python",
                                       "/service/scripts/service_runtime.py", "info",
                                       "--env-file", "/dev/null"])
        write_state(state_path, dict(runtime="docker", launch_id=identity, container=args.name,
                                     image=args.image, base_url=base_url, configuration=configuration))
        print(f"READY docker {args.name} {base_url} | Ctrl+C stops this container", flush=True)
        return process.wait()
    finally:
        # Never stop/delete a container created by somebody else or a later launch.
        labels = inspect_container(args.name)
        if labels is not None and labels.get(label) == identity:
            subprocess.run(["docker", "stop", "--time", "10", args.name],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        terminate_process(process)
        clear_state(state_path, identity)


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for runtime in ("host", "docker"):
        launch = commands.add_parser(runtime)
        launch.add_argument("--env-file", type=Path, default=ROOT / ".env")
        launch.add_argument("--port", type=port_number, default=None if runtime == "host" else 8091)
        launch.add_argument("--startup-timeout", type=positive_seconds, default=120)
        launch.add_argument("--index-url", default=INDEX)
        launch.add_argument("--torch-index-url", default="https://download.pytorch.org/whl/cpu")
        launch.add_argument("--disable-embeddings", action="store_true")
        launch.add_argument("--allow-no-key", action="store_true", help="explicit embeddings-only launch")
        if runtime == "host":
            launch.add_argument("--python", default=shutil.which("python3.12") or "python3.12")
            launch.add_argument("--venv", type=Path, default=ROOT / ".venv")
            launch.add_argument("--no-install", action="store_true")
            launch.add_argument("--no-download", action="store_true")
        else:
            launch.add_argument("--image", default="gorkycode-ai-service:cloudru")
            launch.add_argument("--name", type=container_name, default="gorkycode-ai-service")
            launch.add_argument("--no-build", action="store_true")
    for command in ("info", "probe", "serve", "contract"):
        internal = commands.add_parser(command)
        internal.add_argument("--env-file", type=Path, default=ROOT / ".env")
        internal.add_argument("--pid", type=int)
        internal.add_argument("--launch-id")
        internal.add_argument("--timeout", type=positive_seconds, default=65)
        internal.add_argument("--max-tokens", type=int, default=2500)
    args = parser.parse_args()
    args.env_file = args.env_file.resolve()
    if args.command == "host":
        args.venv = args.venv.resolve()
    return args


def main():
    args = parse_args()
    if args.command == "host":
        return host(args)
    if args.command == "docker":
        return docker(args)
    if args.command == "serve":
        return serve(args)
    return info_in_runtime(args)


if __name__ == "__main__":
    def stop_requested(signum, frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, stop_requested)
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("STOPPED", flush=True)
        sys.exit(130)
    except RuntimeFailure as exc:
        print(json.dumps({"passed": False, "error": str(exc)}), flush=True)
        sys.exit(1)
    except Exception as exc:
        # Validation errors can contain secrets: emit only the exception class.
        print(json.dumps({"passed": False, "error": type(exc).__name__}), flush=True)
        sys.exit(1)
