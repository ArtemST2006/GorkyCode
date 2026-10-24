"""Strict single-text embedding wire models."""

from typing import Literal

from pydantic import StrictStr, field_validator

from app.embeddings.encoder import validate_vector
from app.schemas.base import ContractModel


class EmbeddingRequest(ContractModel):
    purpose: Literal["document", "query"]
    text: StrictStr

    @field_validator("text")
    @classmethod
    def nonblank_text(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("Text must be nonblank")
        return value


class EmbeddingResponse(ContractModel):
    embedding: list[float]
    model: StrictStr
    model_version: StrictStr

    @field_validator("embedding", mode="before")
    @classmethod
    def vector(cls, value):
        if not isinstance(value, (list, tuple)):
            raise ValueError("Expected vector")
        return list(validate_vector(value))

    @field_validator("model", "model_version")
    @classmethod
    def nonblank_identity(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("Identity must be nonblank")
        return value
