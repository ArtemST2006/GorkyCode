"""Period and record-type filters for listing calendar entries."""

from pydantic import Field

from app.schemas.base import ContractModel
from app.schemas.common import ISODate, RecordTypes


class ListEventsDraft(ContractModel):
    date_from: ISODate = Field(default=None)
    date_to_exclusive: ISODate = Field(default=None)
    record_types: RecordTypes = Field(default=None)


class ListEventsAction(ContractModel):
    date_from: ISODate
    date_to_exclusive: ISODate
    record_types: RecordTypes = Field(default=None)
