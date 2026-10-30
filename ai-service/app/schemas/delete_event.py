"""Public on_delete request context and phase-specific actions."""

from datetime import date
from typing import Literal

from pydantic import Field, model_validator

from app.schemas.base import ContractModel
from app.schemas.common import ISODate, LocalTime, RecordType, RecordTypes, SearchQuery, TaskStatus, UUIDString
from app.schemas.messages import RecentMessages


class DeleteSearchDraft(ContractModel):
    title_query: SearchQuery | None = Field(default=None)
    date_from: ISODate | None = Field(default=None)
    date_to_exclusive: ISODate | None = Field(default=None)
    time_from: LocalTime | None = Field(default=None)
    time_to_exclusive: LocalTime | None = Field(default=None)
    record_types: RecordTypes | None = Field(default=None)


class DeleteCandidate(ContractModel):
    number: int = Field(ge=1)
    id: UUIDString
    record_type: RecordType
    title: SearchQuery
    date: ISODate | None
    time: LocalTime | None
    status: TaskStatus

    @model_validator(mode="after")
    def task_only_progress(self):
        if self.status == "in_progress" and self.record_type != "task":
            raise ValueError("Only tasks may be in progress")
        return self


class DeleteCandidateContext(ContractModel):
    recent_messages: RecentMessages
    draft: DeleteSearchDraft
    candidates: list[DeleteCandidate]

    @model_validator(mode="after")
    def unique_candidates(self):
        numbers = [candidate.number for candidate in self.candidates]
        ids = [candidate.id for candidate in self.candidates]
        if len(numbers) != len(set(numbers)) or len(ids) != len(set(ids)):
            raise ValueError("Candidate numbers and IDs must be unique")
        return self


class DeleteClarificationContext(ContractModel):
    recent_messages: RecentMessages
    draft: DeleteSearchDraft


class DeleteFindCandidatesAction(ContractModel):
    phase: Literal["find_candidates"]
    title_query: SearchQuery | None
    date_from: ISODate | None
    date_to_exclusive: ISODate | None
    time_from: LocalTime | None
    time_to_exclusive: LocalTime | None
    record_types: RecordTypes | None

    @model_validator(mode="after")
    def valid_search(self):
        if all(
            value is None
            for value in (
                self.title_query,
                self.date_from,
                self.date_to_exclusive,
                self.time_from,
                self.time_to_exclusive,
                self.record_types,
            )
        ):
            raise ValueError("Candidate search needs at least one trait")
        if self.date_from and self.date_to_exclusive:
            if date.fromisoformat(self.date_from) >= date.fromisoformat(self.date_to_exclusive):
                raise ValueError("Invalid date interval")
        if self.time_from and self.time_to_exclusive:
            if self.time_from >= self.time_to_exclusive:
                raise ValueError("Invalid time interval")
        return self


class DeleteConfirmedAction(ContractModel):
    phase: Literal["delete_confirmed"]
    task_ids: list[UUIDString] = Field(min_length=1)

    @model_validator(mode="after")
    def unique_task_ids(self):
        if len(self.task_ids) != len(set(self.task_ids)):
            raise ValueError("task_ids must be unique")
        return self
