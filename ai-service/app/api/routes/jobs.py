"""Job HTTP endpoint; orchestration belongs to the injected application service."""

from typing import Annotated

from fastapi import APIRouter, Body

from app.api.dependencies import JobServiceDependency
from app.api.openapi import job_request_examples
from app.schemas.job import JobRequest, JobResponse

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
) -> JobResponse:
    return await service.process(payload)
