"""Compatibility entrypoint for the existing Docker Compose service."""

import logging

import uvicorn

from app.core.config import Settings


if __name__ == "__main__":
    settings = Settings()
    logging.basicConfig(level=settings.log_level, format="%(levelname)s %(name)s %(message)s")
    # HTTP access logging includes raw URLs; the app records only matched routes.
    uvicorn.run(
        "app.main:app",
        host="0.0.0.0",
        port=settings.port,
        log_level=settings.log_level.lower(),
        access_log=False,
    )
