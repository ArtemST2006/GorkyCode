"""Validated scalar types shared by calendar requests and actions."""

from datetime import date as calendar_date, datetime
import re
from typing import Annotated, Literal
from uuid import UUID
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from pydantic import (
    AfterValidator,
    AwareDatetime,
    BeforeValidator,
    Field,
    StringConstraints,
    WithJsonSchema,
)


def _uuid(value: str) -> str:
    if not re.fullmatch(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}", value):
        raise ValueError("Expected a UUID in hyphenated form")
    UUID(value)
    return value


def _date(value: str) -> str:
    if not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}", value):
        raise ValueError("Expected a date in YYYY-MM-DD format")
    calendar_date.fromisoformat(value)
    return value


def _timezone(value: str) -> str:
    try:
        ZoneInfo(value)
    except (ZoneInfoNotFoundError, ValueError) as exc:
        raise ValueError("Expected a valid IANA timezone") from exc
    return value


def _request_time(value: object) -> object:
    if isinstance(value, datetime):
        return value
    if not isinstance(value, str) or not re.fullmatch(
        r"[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}"
        r"(?:\.[0-9]+)?(?:[Zz]|[+-][0-9]{2}:[0-9]{2})",
        value,
    ):
        raise ValueError("Expected an RFC 3339 date-time with timezone offset")
    return value


def _unique_record_types(value: list[str]) -> list[str]:
    if len(value) != len(set(value)):
        raise ValueError("record_types must not contain duplicates")
    return value


def _nonblank(value: str) -> str:
    if not value.strip():
        raise ValueError("query must contain a non-whitespace character")
    return value


UUIDString = Annotated[
    str, AfterValidator(_uuid), WithJsonSchema({"type": "string", "format": "uuid"})
]
ISODate = Annotated[
    str, AfterValidator(_date), WithJsonSchema({"type": "string", "format": "date"})
]
NonEmptyString = Annotated[str, StringConstraints(min_length=1)]
Timezone = Annotated[NonEmptyString, AfterValidator(_timezone)]
RequestTime = Annotated[AwareDatetime, BeforeValidator(_request_time)]
RecordType = Literal["task", "reminder", "meeting"]
TaskStatus = Literal["active", "in_progress", "completed"]
RecordTypes = Annotated[
    list[RecordType],
    Field(min_length=1, json_schema_extra={"uniqueItems": True}),
    AfterValidator(_unique_record_types),
]
LocalTime = Annotated[str, StringConstraints(pattern=r"^([01][0-9]|2[0-3]):[0-5][0-9]$")]
SearchQuery = Annotated[NonEmptyString, AfterValidator(_nonblank)]
