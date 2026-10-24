"""Application factory: wire HTTP components and process-scoped dependencies."""

from collections.abc import Callable

from fastapi import FastAPI

from app.api.exception_handlers import register_exception_handlers
from app.api.middleware import EmbedRequestMiddleware, observe_requests
from app.api.openapi import configure_openapi
from app.api.router import router
from app.core.config import Settings
from app.core.logging import configure_logging
from app.lifespan import create_lifespan
from app.llm.protocols import ModelGateway
from app.embeddings import Encoder


def create_app(settings: Settings | None = None, gateway: ModelGateway | None = None,
               *, encoder: Encoder | None = None,
               encoder_factory: Callable[[], Encoder] | None = None) -> FastAPI:
    settings = settings or Settings()
    configure_logging(settings.log_level)
    application = FastAPI(
        title="GorkyCode AI Service API",
        version="1.0.0",
        lifespan=create_lifespan(settings, gateway, encoder, encoder_factory),
    )
    register_exception_handlers(application)
    application.middleware("http")(observe_requests)
    application.add_middleware(EmbedRequestMiddleware,
                               timeout=settings.embedding_timeout_seconds)
    application.include_router(router)
    configure_openapi(application)
    return application


app = create_app()
