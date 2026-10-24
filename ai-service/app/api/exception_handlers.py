"""Translate application and HTTP errors to the public problem contract."""

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.exceptions import HTTPException

from app.core.errors import ServiceError
from app.core.logging import logger
from app.schemas.problem import Problem


def problem_response(
    status: int,
    code: str,
    title: str,
    detail: str,
    instance: str,
    headers: dict[str, str] | None = None,
) -> JSONResponse:
    problem = Problem(
        type="about:blank", title=title, status=status, code=code, detail=detail, instance=instance
    )
    return JSONResponse(
        problem.model_dump(exclude_unset=True),
        status_code=status,
        media_type="application/problem+json",
        headers=headers,
    )


async def service_error(request: Request, exc: ServiceError) -> JSONResponse:
    logger.warning("service_error status=%s code=%s", exc.status_code, exc.code)
    return problem_response(exc.status_code, exc.code, exc.title, exc.detail, request.url.path)


async def validation_error(request: Request, exc: RequestValidationError) -> JSONResponse:
    return problem_response(
        400,
        "invalid_request",
        "Invalid request",
        "Malformed JSON or request fields violate the API contract.",
        request.url.path,
    )


async def http_error(request: Request, exc: HTTPException) -> JSONResponse:
    return problem_response(
        exc.status_code,
        "http_error",
        "HTTP error",
        "The requested operation could not be performed.",
        request.url.path,
        headers=exc.headers,
    )


def register_exception_handlers(application: FastAPI) -> None:
    application.add_exception_handler(ServiceError, service_error)
    application.add_exception_handler(RequestValidationError, validation_error)
    application.add_exception_handler(HTTPException, http_error)
