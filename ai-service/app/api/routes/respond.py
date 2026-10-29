"""Describe backend results with fixed templates; no model calls or actions."""

from fastapi import APIRouter

from app.schemas.respond import RespondRequest, RespondResponse
from app.services.result_messages import result_message

router = APIRouter(prefix="/api", tags=["AI"])


@router.post("/respond", response_model=RespondResponse, operation_id="respondToResult")
async def respond_to_result(payload: RespondRequest) -> RespondResponse:
    return RespondResponse(
        user_id=payload.user_id,
        task_id=payload.task_id,
        message=result_message(payload),
    )
