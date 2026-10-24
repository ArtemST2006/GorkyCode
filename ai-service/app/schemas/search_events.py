"""Title search and optional calendar filters."""

from pydantic import Field

from app.schemas.base import ContractModel
from app.schemas.common import ISODate, NonEmptyString, RecordTypes, SearchQuery


class SearchEventsDraft(ContractModel):
    query: NonEmptyString = Field(default=None)
    date_from: ISODate = Field(default=None)
    date_to_exclusive: ISODate = Field(default=None)
    record_types: RecordTypes = Field(default=None)


class SearchEventsAction(ContractModel):
    query: SearchQuery
    date_from: ISODate = Field(default=None)
    date_to_exclusive: ISODate = Field(default=None)
    record_types: RecordTypes = Field(default=None)
