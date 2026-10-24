"""Job HTTP endpoint; orchestration belongs to the injected application service."""

from typing import Annotated

from fastapi import APIRouter, Body, Response

from app.api.dependencies import JobServiceDependency
from app.api.openapi import job_request_examples
from app.schemas.job import (
    DeleteConfirmedResponse,
    DeleteFindCandidatesResponse,
    DeleteNeedsClarificationResponse,
    JobRequest,
    JobResponse,
)

router = APIRouter(prefix="/api", tags=["AI"])


@router.post(
    "/job",
    response_model=JobResponse,
    response_model_exclude_unset=True,
    operation_id="processJob",
)
async def process_job(
    payload: Annotated[JobRequest, Body(openapi_examples=job_request_examples())],
    service: JobServiceDependency,
    response: Response,
) -> JobResponse:
    result = await service.process(payload)
    if isinstance(
        result,
        (DeleteNeedsClarificationResponse, DeleteFindCandidatesResponse, DeleteConfirmedResponse),
    ):
        response.headers["X-Task-Intent"] = "on_delete"
    return result
