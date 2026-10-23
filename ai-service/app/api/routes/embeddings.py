"""Single-text embedding endpoint; CPU work is owned by the service."""

from fastapi import APIRouter, Response

from app.api.dependencies import EmbeddingServiceDependency
from app.schemas.embedding import EmbeddingRequest, EmbeddingResponse

router = APIRouter(prefix="/api", tags=["AI"])


@router.post("/embed", response_model=EmbeddingResponse, operation_id="embedText")
async def embed_text(payload: EmbeddingRequest, response: Response,
                     service: EmbeddingServiceDependency) -> EmbeddingResponse:
    result = await service.process(payload)
    response.headers["Cache-Control"] = "no-store"
    return result
