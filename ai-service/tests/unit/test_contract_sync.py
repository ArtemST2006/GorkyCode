"""The root contract is authoritative; mirrors and synthetic examples follow it."""

import json
import math
from pathlib import Path


SERVICE_ROOT = Path(__file__).parents[2]


def test_contract_mirrors_equal_authoritative_bytes():
    authoritative = (SERVICE_ROOT.parent / "contracts" / "ai.openapi.yaml").read_bytes()
    for mirror in ("contracts/ai.openapi.yaml", "app/resources/ai.openapi.yaml"):
        assert (SERVICE_ROOT / mirror).read_bytes() == authoritative, (
            "Copy the authoritative contracts/ai.openapi.yaml to both service mirrors"
        )


def test_synthetic_embed_wire_fixtures_follow_contract(assert_contract):
    fixtures = json.loads((SERVICE_ROOT / "tests/fixtures/embed-wire.json").read_text())
    for purpose in ("document", "query"):
        assert_contract(fixtures["requests"][purpose], "EmbeddingRequest")
        response = fixtures["responses"][purpose]
        assert_contract(response, "EmbeddingResponse")
        assert len(response["embedding"]) == 384
        assert math.isclose(sum(value * value for value in response["embedding"]), 1.0)
        assert response["model"] == "synthetic-wire-fixture"
    assert fixtures["success_headers"]["Cache-Control"] == "no-store"
    for response in fixtures["problem_responses"]:
        assert_contract(response["body"], "Problem")
