"""Contract fixtures for schema validation; no gateway or encoder substitutes."""

from pathlib import Path

from openapi_schema_validator import OAS30Validator
import pytest
import yaml
from referencing import Registry, Resource
from referencing.jsonschema import DRAFT4


@pytest.fixture
def contract():
    path = Path(__file__).parents[2] / "contracts" / "ai.openapi.yaml"
    return yaml.safe_load(path.read_text(encoding="utf-8"))


@pytest.fixture
def assert_contract(contract):
    registry = Registry().with_resource(
        "urn:gorkycode:contract", Resource.from_contents(contract, default_specification=DRAFT4)
    )

    def validate(value, schema_name):
        schema = {"$ref": f"urn:gorkycode:contract#/components/schemas/{schema_name}"}
        # OAS 3.0 nullable semantics differ from JSON Schema / OpenAPI 3.1.
        OAS30Validator(schema, registry=registry).validate(value)

    return validate


@pytest.fixture
def make_decision():
    def make(
        *,
        status="ready",
        intent="create_event",
        action=None,
        draft=None,
        message=None,
        topic_changed=False,
    ):
        return {
            "status": status,
            "intent": intent,
            "message": (
                "Запрос выполнен." if status == "ready" and message is None else message
            ),
            "action": action,
            "draft": draft,
            "topic_changed": topic_changed,
        }

    return make
