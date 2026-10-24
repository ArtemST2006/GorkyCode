"""Compose endpoint groups without changing contract paths."""

from fastapi import APIRouter

from app.api.routes.embeddings import router as embeddings_router
from app.api.routes.health import router as health_router
from app.api.routes.jobs import router as jobs_router

router = APIRouter()
router.include_router(health_router)
router.include_router(jobs_router)

router.include_router(embeddings_router)
