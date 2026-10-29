"""Bound streaming bodies and effective context without truncating commands."""

import asyncio
from copy import deepcopy

import pytest

from app.core.config import Settings
from app.core.errors import ServiceError
from app.schemas.job import JobRequest
from app.security.limits import RequestBodyLimits, validate_job_limits


def request(context=None, text="Найди задачи"):
    return JobRequest.model_validate({
        "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
        "task_id": "70ce187e-a2a6-4499-91a0-5c61e5920b51",
        "text": text, "request_time": "2026-10-29T12:00:00+03:00",
        "timezone": "Europe/Moscow", "context": context or {},
    })


def test_limits_apply_after_last_twenty_history_messages():
    history = [{"role": "user", "text": "x" * 10000}]
    history += [{"role": "user", "text": "короткое сообщение"} for _ in range(20)]
    value = request({"recent_messages": history, "draft": {"title": "задача"}})
    before = deepcopy(value)
    validate_job_limits(value, Settings(_env_file=None))
    assert value == before


@pytest.mark.parametrize("context,text", [
    ({}, "x" * 8193),
    ({"recent_messages": [{"role": "user", "text": "x" * 8193}], "draft": {}}, "ok"),
    ({"recent_messages": [{"role": "user", "text": "ok"}], "draft": {"title": "x" * 33000}}, "ok"),
])
def test_oversize_command_history_and_draft_rejected(context, text):
    with pytest.raises(ServiceError) as exc:
        validate_job_limits(request(context, text), Settings(_env_file=None))
    assert exc.value.status_code == 413


def test_body_limit_rejects_before_app_without_length_and_stops_reading():
    async def check():
        reads = []
        sent = []

        async def app(scope, receive, send):
            raise AssertionError("app must not parse oversized body")

        async def receive():
            reads.append(True)
            return {"type": "http.request", "body": b"x" * 6, "more_body": True}

        async def send(message):
            sent.append(message)

        middleware = RequestBodyLimits(app, job_max_bytes=10, respond_max_bytes=10,
                                       timeout_seconds=1)
        await middleware({"type": "http", "method": "POST", "path": "/api/job", "headers": []},
                         receive, send)
        assert len(reads) == 2
        assert sent[0]["status"] == 413
    asyncio.run(check())


def test_body_replay_and_other_route_passthrough():
    async def check():
        sent = []
        bodies = []
        chunks = iter([{"type": "http.request", "body": b"abc", "more_body": True},
                       {"type": "http.request", "body": b"def", "more_body": False}])

        async def receive():
            return next(chunks)

        async def send(message):
            sent.append(message)

        async def app(scope, receive, send):
            bodies.append(await receive())

        middleware = RequestBodyLimits(app, job_max_bytes=10, respond_max_bytes=10,
                                       timeout_seconds=1)
        await middleware({"type": "http", "method": "POST", "path": "/api/job", "headers": []},
                         receive, send)
        assert bodies == [{"type": "http.request", "body": b"abcdef", "more_body": False}]
    asyncio.run(check())
