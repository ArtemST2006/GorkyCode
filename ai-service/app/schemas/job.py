"""Job request and discriminated public response envelopes."""

from typing import Annotated, Literal, Union

from pydantic import AfterValidator, BaseModel, ConfigDict, Field, TypeAdapter

from app.schemas.base import ContractModel
from app.schemas.common import NonEmptyString, RequestTime, Timezone, UUIDString
from app.schemas.context import (
    CreateEventContext,
    JobRequestContext,
    ListEventsContext,
    ReadyContext,
    SearchEventsContext,
)
from app.schemas.create_event import CreateEventAction
from app.schemas.list_events import ListEventsAction
from app.schemas.search_events import SearchEventsAction


class JobRequest(ContractModel):
    user_id: UUIDString
    task_id: UUIDString
    text: NonEmptyString
    request_time: RequestTime
    timezone: Timezone
    context: JobRequestContext


class ClarificationBase(BaseModel):
    # The response envelope is open in the original allOf schema.
    model_config = ConfigDict(extra="allow")
    user_id: UUIDString
    task_id: UUIDString
    status: Literal["needs_clarification"]
    message: NonEmptyString
    action: None


class ClarificationCreateEvent(ClarificationBase):
    intent: Literal["create_event"]
    context: CreateEventContext


class ClarificationListEvents(ClarificationBase):
    intent: Literal["list_events"]
    context: ListEventsContext


class ClarificationSearchEvents(ClarificationBase):
    intent: Literal["search_events"]
    context: SearchEventsContext


NeedsClarificationResponse = Annotated[
    Union[ClarificationCreateEvent, ClarificationListEvents, ClarificationSearchEvents],
    Field(discriminator="intent"),
]


def _ready_message(value: str) -> str:
    if not value.strip():
        raise ValueError("Ready message must contain a non-whitespace character")
    return value


class ReadyBase(BaseModel):
    model_config = ConfigDict(extra="allow")
    user_id: UUIDString
    task_id: UUIDString
    status: Literal["ready"]
    message: Annotated[NonEmptyString, AfterValidator(_ready_message)]
    context: ReadyContext


class ReadyCreateEvent(ReadyBase):
    intent: Literal["create_event"]
    action: CreateEventAction


class ReadyListEvents(ReadyBase):
    intent: Literal["list_events"]
    action: ListEventsAction


class ReadySearchEvents(ReadyBase):
    intent: Literal["search_events"]
    action: SearchEventsAction


ReadyResponse = Annotated[
    Union[ReadyCreateEvent, ReadyListEvents, ReadySearchEvents], Field(discriminator="intent")
]
JobResponse = Annotated[
    Union[NeedsClarificationResponse, ReadyResponse], Field(discriminator="status")
]
JOB_RESPONSE_ADAPTER = TypeAdapter(JobResponse)
