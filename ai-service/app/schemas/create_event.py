"""Partial and complete inputs for creating calendar entries."""

from typing import Annotated, Literal, Union

from pydantic import Field

from app.schemas.base import ContractModel
from app.schemas.common import ISODate, LocalTime, NonEmptyString, RecordType


class CreateEventDraft(ContractModel):
    record_type: RecordType = Field(default=None)
    title: NonEmptyString = Field(default=None)
    date: ISODate = Field(default=None)
    time: LocalTime = Field(default=None)


class TaskAction(ContractModel):
    record_type: Literal["task"]
    title: NonEmptyString
    date: ISODate
    time: LocalTime


class TimedEventAction(ContractModel):
    record_type: Literal["meeting", "reminder"]
    title: NonEmptyString
    date: ISODate
    time: LocalTime


CreateEventAction = Annotated[
    Union[TaskAction, TimedEventAction], Field(discriminator="record_type")
]
