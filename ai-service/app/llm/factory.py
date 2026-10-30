"""Construct the sole Cloud.ru model adapter without changing the public API."""

from app.core.config import Settings
from app.llm.cloudru import CloudRuGateway


def create_gateway(settings: Settings) -> CloudRuGateway:
    return CloudRuGateway(settings)


def create_guard_gateway(settings: Settings) -> CloudRuGateway | None:
    """Use an explicitly configured guard model and a separate owned client."""
    if not settings.security_guard_model.strip():
        return None
    return CloudRuGateway(settings.model_copy(update={
        "cloudru_model": settings.security_guard_model.strip(),
        "cloudru_timeout_seconds": settings.security_guard_timeout_seconds,
        "cloudru_max_tokens": settings.security_guard_max_tokens,
        "cloudru_max_retries": 0,
    }))
