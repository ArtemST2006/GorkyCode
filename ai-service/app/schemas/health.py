"""Process liveness response."""

from typing import Literal

from app.schemas.base import ContractModel


class AIHealthResponse(ContractModel):
    service: Literal["ai-service"]
    status: Literal["ok"]
