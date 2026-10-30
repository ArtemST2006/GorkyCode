"""Period and record-type filters for listing calendar entries."""

from pydantic import Field

from app.schemas.base import ContractModel
from app.schemas.common import ISODate, RecordTypes, TaskStatus


class ListEventsDraft(ContractModel):
    date_from: ISODate = Field(default=None)
    date_to_exclusive: ISODate = Field(default=None)
    record_types: RecordTypes = Field(default=None)
    statuses: list[TaskStatus] = Field(default=None, min_length=1)
    upcoming_minutes: int = Field(default=None, ge=1, le=10080)


class ListEventsAction(ContractModel):
    date_from: ISODate = Field(default=None)
    date_to_exclusive: ISODate = Field(default=None)
    record_types: RecordTypes = Field(default=None)
    statuses: list[TaskStatus] = Field(default=None, min_length=1)
    upcoming_minutes: int = Field(default=None, ge=1, le=10080)
