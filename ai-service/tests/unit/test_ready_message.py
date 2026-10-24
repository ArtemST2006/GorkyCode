"""A prepared message is required; database results never enter AI decisions."""

from copy import deepcopy

from openapi_schema_validator import OAS30Validator
from pydantic import ValidationError
import pytest
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT4
import yaml

from app.llm.schemas import ModelDecision
from app.schemas.job import JOB_RESPONSE_ADAPTER


@pytest.mark.parametrize("message", [None, "", "   ", "\n\t"])
def test_ready_rejects_missing_or_blank_message(message, make_decision, contract):
    example = deepcopy(
        contract["paths"]["/api/job"]["post"]["responses"]["200"]["content"]
        ["application/json"]["examples"]["readySearchEvents"]["value"]
    )
    example["message"] = message
    with pytest.raises(ValidationError):
        JOB_RESPONSE_ADAPTER.validate_python(example)
    with pytest.raises(ValidationError):
        ModelDecision.model_validate(
            make_decision(intent="search_events", action={"query": "Иван"}, message="valid")
            | {"message": message}
        )


def test_unsupported_retains_nulls(make_decision):
    values = make_decision(status="unsupported", intent=None)
    assert ModelDecision.model_validate(values).message is None
    for field in ("message", "action", "draft", "intent"):
        with pytest.raises(ValidationError):
            ModelDecision.model_validate(values | {field: "unexpected"})


def test_collection_message_does_not_depend_on_database_results(contract):
    # Backend forwards the same AI message unchanged for zero or more results.
    from pathlib import Path

    backend = yaml.safe_load(
        (Path(__file__).parents[3] / "contracts/backend.openapi.yaml").read_text()
    )
    registry = Registry().with_resource(
        "urn:backend", Resource.from_contents(backend, default_specification=DRAFT4)
    )
    validator = OAS30Validator(
        {"$ref": "urn:backend#/components/schemas/TaskCollectionResponse"}, registry=registry
    )
    message = contract["paths"]["/api/job"]["post"]["responses"]["200"]["content"][
        "application/json"
    ]["examples"]["readySearchEvents"]["value"]["message"]
    example = deepcopy(
        backend["paths"]["/api/task"]["post"]["responses"]["200"]["content"]
        ["application/json"]["examples"]["listEvents"]["value"]
    )
    example["message"] = message
    validator.validate(example)
    example["data"]["items"] = []
    validator.validate(example)
