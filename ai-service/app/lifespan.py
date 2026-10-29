"""Own process-scoped resources and compose the application service."""

import asyncio

from collections.abc import AsyncIterator, Callable
from contextlib import AbstractAsyncContextManager, asynccontextmanager

from fastapi import FastAPI

from app.core.config import Settings
from app.core.logging import logger
from app.embeddings import Encoder, E5Encoder
from app.services.embeddings import EmbeddingService
from app.llm.factory import create_gateway, create_guard_gateway
from app.llm.protocols import ModelGateway
from app.services.jobs import JobService
from app.security.action_guard import ActionGuard
from app.security.guard_protocols import ActionVerifier


def create_lifespan(
    settings: Settings, gateway: ModelGateway | None = None,
    encoder: Encoder | None = None, encoder_factory: Callable[[], Encoder] | None = None,
    guard: ActionVerifier | None = None,
) -> Callable[[FastAPI], AbstractAsyncContextManager[None]]:
    @asynccontextmanager
    async def lifespan(application: FastAPI) -> AsyncIterator[None]:
        owned_gateway = create_gateway(settings) if gateway is None else None
        model = owned_gateway if owned_gateway is not None else gateway
        assert model is not None
        owned_guard_gateway = create_guard_gateway(settings) if guard is None else None
        active_guard = guard
        if owned_guard_gateway is not None:
            active_guard = ActionGuard(owned_guard_gateway,
                                       timeout_seconds=settings.security_guard_timeout_seconds)
        application.state.job_service = JobService(settings, model, active_guard)
        active_encoder = encoder
        if active_encoder is None and settings.embedding_enabled:
            factory = encoder_factory or (lambda: E5Encoder(
                settings.embedding_model_path, num_threads=settings.embedding_num_threads))
            try:
                active_encoder = await asyncio.to_thread(factory)
            except Exception:
                # /health and /api/job remain available when weights are absent.
                logger.error("embedding_startup_unavailable")
        application.state.embedding_service = EmbeddingService(
            active_encoder, max_workers=settings.embedding_max_workers,
            timeout=settings.embedding_timeout_seconds)
        try:
            yield
        finally:
            application.state.embedding_service.close()
            # An injected gateway belongs to the caller; close only our own client.
            if owned_gateway is not None:
                await owned_gateway.close()
            if owned_guard_gateway is not None:
                await owned_guard_gateway.close()

    return lifespan
