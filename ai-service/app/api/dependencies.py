"""FastAPI dependencies; lookups are async and never use the thread pool."""

from typing import Annotated, cast

from fastapi import Depends, Request

from app.services.jobs import JobService
from app.services.embeddings import EmbeddingService


async def get_job_service(request: Request) -> JobService:
    return cast(JobService, request.app.state.job_service)


JobServiceDependency = Annotated[JobService, Depends(get_job_service)]


async def get_embedding_service(request: Request) -> EmbeddingService:
    return request.app.state.embedding_service


EmbeddingServiceDependency = Annotated[EmbeddingService, Depends(get_embedding_service)]
