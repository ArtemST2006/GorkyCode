"""Shared configuration for closed contract objects."""

from pydantic import BaseModel, ConfigDict


def _hide_missing_defaults(schema: dict) -> None:
    """Do not advertise the internal absence marker as a JSON default."""
    for field_schema in schema.get("properties", {}).values():
        if field_schema.get("default", ...) is None and field_schema.get("type") != "null":
            field_schema.pop("default", None)


class ContractModel(BaseModel):
    model_config = ConfigDict(extra="forbid", json_schema_extra=_hide_missing_defaults)
