"""RFC 9457 technical error payload."""

from typing import Annotated

from pydantic import Field

from app.schemas.base import ContractModel


class Problem(ContractModel):
    type: str
    title: str
    status: Annotated[int, Field(strict=True, ge=-(2**31), le=2**31 - 1)]
    code: str
    detail: str = Field(default=None)
    instance: str = Field(default=None)
