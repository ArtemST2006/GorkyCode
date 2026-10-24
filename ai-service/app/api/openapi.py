"""Publish the supplied OpenAPI 3.0.3 contract without relabelling 3.1 schemas."""

from copy import deepcopy
from functools import lru_cache
from importlib.resources import files
from typing import Any

from fastapi import FastAPI
import yaml


@lru_cache(maxsize=1)
def load_contract() -> dict[str, Any]:
    return yaml.safe_load(
        files("app").joinpath("resources/ai.openapi.yaml").read_text(encoding="utf-8")
    )


def job_request_examples() -> dict[str, Any]:
    return deepcopy(
        load_contract()["paths"]["/api/job"]["post"]["requestBody"]["content"]["application/json"][
            "examples"
        ]
    )


def configure_openapi(application: FastAPI) -> None:
    application.openapi = lambda: deepcopy(load_contract())
