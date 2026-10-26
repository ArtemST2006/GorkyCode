"""Two-stage on_update dialogue and its public actions."""

from datetime import date
from typing import Literal

from pydantic import Field, model_validator

from app.schemas.base import ContractModel
from app.schemas.common import ISODate, LocalTime, RecordType, RecordTypes, SearchQuery, UUIDString
from app.schemas.messages import RecentMessages


class UpdateDraftChanges(ContractModel):
    title: SearchQuery | None = None
    date: ISODate | None = None
    time: LocalTime | None = None
    status: Literal["active", "completed"] | None = None


class UpdateChanges(UpdateDraftChanges):
    @model_validator(mode="after")
    def nonempty_and_nonnull(self):
        if not self.model_fields_set or any(
            getattr(self, name) is None for name in self.model_fields_set
        ):
            raise ValueError("Update needs at least one non-null change")
        return self


class UpdateSearchDraft(ContractModel):
    title_query: SearchQuery | None = None
    date_from: ISODate | None = None
    date_to_exclusive: ISODate | None = None
    time_from: LocalTime | None = None
    time_to_exclusive: LocalTime | None = None
    record_types: RecordTypes | None = None
    changes: UpdateDraftChanges | None = None


class UpdateFindCandidatesAction(ContractModel):
    phase: Literal["find_candidates"]
    title_query: SearchQuery | None
    date_from: ISODate | None
    date_to_exclusive: ISODate | None
    time_from: LocalTime | None
    time_to_exclusive: LocalTime | None
    record_types: RecordTypes | None
    changes: UpdateChanges

    @model_validator(mode="after")
    def valid_search(self):
        if all(value is None for value in (
            self.title_query, self.date_from, self.date_to_exclusive,
            self.time_from, self.time_to_exclusive, self.record_types,
        )):
            raise ValueError("Update candidate search needs at least one trait")
        if self.date_from and self.date_to_exclusive:
            if date.fromisoformat(self.date_from) >= date.fromisoformat(self.date_to_exclusive):
                raise ValueError("Invalid date interval")
        if self.time_from and self.time_to_exclusive:
            if self.time_from >= self.time_to_exclusive:
                raise ValueError("Invalid time interval")
        return self


class UpdateConfirmedAction(ContractModel):
    phase: Literal["update_confirmed"]
    record_ids: list[UUIDString] = Field(min_length=1)

    @model_validator(mode="after")
    def unique_ids(self):
        if len(self.record_ids) != len(set(self.record_ids)):
            raise ValueError("record_ids must be unique")
        return self


class UpdateCandidate(ContractModel):
    number: int = Field(ge=1)
    id: UUIDString
    record_type: RecordType
    title: SearchQuery
    date: ISODate | None
    time: LocalTime | None
    status: Literal["active", "completed"]


class UpdateCandidateContext(ContractModel):
    recent_messages: RecentMessages
    draft: UpdateSearchDraft
    candidates: list[UpdateCandidate]

    @model_validator(mode="after")
    def unique_candidates(self):
        numbers = [candidate.number for candidate in self.candidates]
        ids = [candidate.id for candidate in self.candidates]
        if len(numbers) != len(set(numbers)) or len(ids) != len(set(ids)):
            raise ValueError("Candidate numbers and IDs must be unique")
        return self


class UpdateClarificationContext(ContractModel):
    recent_messages: RecentMessages
    draft: UpdateSearchDraft
