"""Construct the sole Cloud.ru model adapter without changing the public API."""

from app.core.config import Settings
from app.llm.cloudru import CloudRuGateway


def create_gateway(settings: Settings) -> CloudRuGateway:
    return CloudRuGateway(settings)
