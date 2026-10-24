"""Application log levels and mandatory redaction-by-omission for SDK logs."""

import logging

logger = logging.getLogger("ai_service")


def configure_logging(level: str) -> None:
    # SDK DEBUG output contains full model messages; it must not inherit DEBUG.
    for library in ("openai", "httpx", "httpcore"):
        logging.getLogger(library).setLevel(logging.WARNING)
    logger.setLevel(level)
