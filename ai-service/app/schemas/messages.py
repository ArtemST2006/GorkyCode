"""Conversation messages shared by request and response contexts."""

from typing import Annotated, Literal

from pydantic import Field

from app.schemas.base import ContractModel
from app.schemas.common import NonEmptyString


class DialogueMessage(ContractModel):
    role: Literal["user", "assistant"]
    text: NonEmptyString


RecentMessages = Annotated[list[DialogueMessage], Field(min_length=1)]
