"""Process liveness; no model request is made."""

from fastapi import APIRouter

from app.schemas.health import AIHealthResponse

router = APIRouter(tags=["Service"])


@router.get("/health", response_model=AIHealthResponse, operation_id="aiHealth")
async def health() -> AIHealthResponse:
    return AIHealthResponse(service="ai-service", status="ok")
