"""Offline integration checks for authorization after public normalization."""

import asyncio
import json

import pytest

from app.core.config import Settings
from app.core.errors import ServiceError
from app.llm.factory import create_guard_gateway
from app.main import create_app
from app.schemas.job import JobRequest
from app.security.guard_schemas import GuardVerdict
from app.services.jobs import JobService


def request():
    return JobRequest.model_validate({
        "user_id": "b80e93e1-9945-48e0-8460-3ee68bcfb402",
        "task_id": "70ce187e-a2a6-4499-91a0-5c61e5920b51",
        "text": "Создай задачу купить билеты завтра",
        "request_time": "2026-10-29T12:00:00+03:00", "timezone": "Europe/Moscow",
        "context": {},
    })


def decision(**changes):
    return {
        "status": "ready", "intent": "create_event", "message": "INJECTED MESSAGE",
        "action": {"record_type": "task", "title": "Купить билеты", "date": "2026-10-30"},
        "draft": None, "topic_changed": False,
    } | changes


class Gateway:
    def __init__(self, result):
        self.result = result
        self.calls = 0

    async def complete(self, messages):
        self.calls += 1
        return json.dumps(self.result)


class Guard:
    def __init__(self, verdict=None, error=None):
        self.verdict = verdict or GuardVerdict(decision="allow", reason_code="consistent_request")
        self.error = error
        self.proposals = []

    async def verify(self, request, *, intent, proposal):
        self.proposals.append((intent, proposal))
        if self.error:
            raise self.error
        return self.verdict


def settings(**values):
    return Settings(_env_file=None, **values)


def test_default_enforce_fails_closed_and_does_not_retry():
    gateway = Gateway(decision())
    with pytest.raises(ServiceError) as exc:
        asyncio.run(JobService(settings(), gateway).process(request()))
    assert (exc.value.status_code, exc.value.code) == (503, "guard_not_configured")
    assert gateway.calls == 1


@pytest.mark.parametrize("verdict", [
    GuardVerdict(decision="block", reason_code="intent_mismatch"),
    GuardVerdict(decision="clarify", reason_code="ambiguous_request"),
])
def test_guard_rejection_does_not_repair_or_return_rejected_state(verdict):
    gateway = Gateway(decision())
    with pytest.raises(ServiceError) as exc:
        asyncio.run(JobService(settings(), gateway, Guard(verdict)).process(request()))
    assert (exc.value.status_code, exc.value.code) == (400, "unsupported_intent")
    assert gateway.calls == 1
    assert "INJECTED" not in str(exc.value)


def test_allow_returns_same_final_proposal_and_template_message():
    guard = Guard()
    response = asyncio.run(JobService(settings(), Gateway(decision()), guard).process(request()))
    assert guard.proposals[0][1] is response
    assert response.action.title == "Купить билеты"
    assert "INJECTED" not in response.message
    assert "Купить билеты" not in response.message


def test_deadline_promoted_draft_is_verified_as_ready():
    guard = Guard()
    gateway = Gateway(decision(status="needs_clarification", action=None,
                               draft={"record_type": "task", "title": "Билеты", "date": "2026-10-30"}))
    response = asyncio.run(JobService(settings(), gateway, guard).process(request()))
    assert response.status == "ready"
    assert guard.proposals[0][1].action.title == "Билеты"


def test_read_only_and_observe_behavior_are_explicit():
    gateway = Gateway(decision(intent="search_events", action={"query": "билеты"}))
    guard = Guard(error=RuntimeError("do not call"))
    response = asyncio.run(JobService(settings(), gateway, guard).process(request()))
    assert response.status == "ready" and not guard.proposals
    response = asyncio.run(JobService(settings(security_guard_mode="observe"),
                                      Gateway(decision())).process(request()))
    assert response.status == "ready"


def test_provider_error_text_never_leaks():
    guard = Guard(error=ServiceError(500, "USER SECRET", "secret", "sensitive provider body"))
    with pytest.raises(ServiceError) as exc:
        asyncio.run(JobService(settings(), Gateway(decision()), guard).process(request()))
    assert exc.value.code == "guard_unavailable"
    assert exc.value.status_code == 503


def test_guard_timeout_does_not_lose_concurrency_slot():
    class SlowGuard:
        async def verify(self, request, *, intent, proposal):
            await asyncio.sleep(10)

    async def check():
        service = JobService(settings(security_guard_timeout_seconds=.01,
                                      security_guard_max_concurrency=1),
                             Gateway(decision()), SlowGuard())
        for _ in range(2):
            with pytest.raises(ServiceError) as exc:
                await service.process(request())
            assert exc.value.status_code == 503
            assert service._guard_slots._value == 1
    asyncio.run(check())


def test_guard_gateway_is_separate_and_requires_explicit_model():
    assert create_guard_gateway(settings()) is None
    configured = settings(security_guard_model="explicit-model", cloudru_api_key=None)
    gateway = create_guard_gateway(configured)
    assert gateway.settings is not configured
    assert gateway.settings.cloudru_model == "explicit-model"
    assert gateway.settings.cloudru_max_retries == 0
    assert gateway.settings.cloudru_max_tokens == configured.security_guard_max_tokens


def test_lifespan_closes_only_owned_gateways(monkeypatch):
    class Client(Gateway):
        def __init__(self):
            super().__init__(decision())
            self.closed = 0

        async def close(self):
            self.closed += 1

    async def check():
        primary, secondary = Client(), Client()
        monkeypatch.setattr("app.lifespan.create_gateway", lambda _: primary)
        monkeypatch.setattr("app.lifespan.create_guard_gateway", lambda _: secondary)
        app = create_app(settings(embedding_enabled=False))
        async with app.router.lifespan_context(app):
            assert app.state.job_service.guard is not None
        assert primary.closed == secondary.closed == 1
        injected, injected_guard = Client(), Guard()
        app = create_app(settings(embedding_enabled=False), injected, guard=injected_guard)
        async with app.router.lifespan_context(app):
            assert app.state.job_service.guard is injected_guard
        assert injected.closed == 0
        assert primary.closed == secondary.closed == 1
    asyncio.run(check())


def test_generated_history_overflow_rejected_before_guard():
    value = request().model_copy(update={"context": JobRequest.model_validate({
        **request().model_dump(mode="json"),
        "context": {"recent_messages": [{"role": "user", "text": "x" * 950}], "draft": {}},
    }).context})
    guard = Guard()
    with pytest.raises(ServiceError) as exc:
        asyncio.run(JobService(settings(security_context_max_chars=1024),
                               Gateway(decision()), guard).process(value))
    assert exc.value.status_code == 413
    assert not guard.proposals
