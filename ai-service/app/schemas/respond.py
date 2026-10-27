"""An answer based on work already completed by the backend."""

from typing import Any, Literal
from pydantic import Field, model_validator
from app.schemas.base import ContractModel
from app.schemas.common import NonEmptyString, UUIDString, Timezone


class RespondRequest(ContractModel):
    user_id: UUIDString
    task_id: UUIDString
    text: NonEmptyString
    timezone: Timezone
    intent: Literal["create_event", "list_events", "search_events", "on_delete", "on_update"]
    status: Literal["ok", "candidates", "deleted", "update_candidates", "updated"]
    data: dict[str, Any]

    @model_validator(mode="after")
    def matches_operation(self):
        variants = {
            ("create_event", "ok"): {"event"},
            ("list_events", "ok"): {"items"},
            ("search_events", "ok"): {"items"},
            ("on_delete", "candidates"): {"candidates"},
            ("on_delete", "deleted"): {"deleted_events"},
            ("on_update", "update_candidates"): {"candidates", "changes"},
            ("on_update", "updated"): {"updated_events"},
        }
        if set(self.data) != variants.get((self.intent, self.status)):
            raise ValueError("data/status do not match the performed operation")
        for key, value in self.data.items():
            if key in ("event", "changes"):
                if not isinstance(value, dict):
                    raise ValueError("Expected an object")
            elif not isinstance(value, list) or any(not isinstance(item, dict) for item in value):
                raise ValueError("Expected a list of objects")
        return self


class RespondResponse(ContractModel):
    user_id: UUIDString
    task_id: UUIDString
    message: NonEmptyString = Field(max_length=6000)
