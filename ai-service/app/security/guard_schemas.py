"""Closed internal verdict: no model-authored text crosses this boundary."""

from typing import Literal

from pydantic import BaseModel, ConfigDict, model_validator


GuardDecision = Literal["allow", "clarify", "block"]
GuardReason = Literal[
    "consistent_request", "insufficient_context", "ambiguous_request",
    "intent_mismatch", "unrequested_change", "instruction_in_data",
    "negated_or_question", "unsupported_action",
]


class GuardVerdict(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True, frozen=True)

    decision: GuardDecision
    reason_code: GuardReason

    @model_validator(mode="after")
    def reason_matches_decision(self) -> "GuardVerdict":
        expected = {
            "allow": {"consistent_request"},
            "clarify": {"insufficient_context", "ambiguous_request"},
            "block": {
                "intent_mismatch", "unrequested_change", "instruction_in_data",
                "negated_or_question", "unsupported_action",
            },
        }
        if self.reason_code not in expected[self.decision]:
            raise ValueError("Reason code does not match decision")
        return self
