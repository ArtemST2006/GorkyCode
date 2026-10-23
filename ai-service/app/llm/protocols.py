"""Provider-independent boundary used by the job service."""

from typing import Literal, Protocol, TypedDict


class ChatMessage(TypedDict):
    role: Literal["system", "user", "assistant"]
    content: str


class ModelGateway(Protocol):
    async def complete(self, messages: list[ChatMessage]) -> str:
        """Return the model final content, or raise a safe service error."""
        ...
