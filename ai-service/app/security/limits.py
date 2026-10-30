"""Reject oversized inputs without silently changing commands or drafts."""

import json

from app.core.config import Settings
from app.core.errors import ServiceError
from app.llm.messages import project_context_for_model
from app.schemas.job import JobRequest, JobResponse


def validate_job_limits(request: JobRequest, settings: Settings) -> None:
    if len(request.text) > settings.security_user_max_chars:
        _too_large()
    _validate_context(project_context_for_model(request.context), settings)


def validate_response_limits(response: JobResponse, settings: Settings) -> None:
    # Appending the current turn must not emit a context that the next request
    # cannot accept. Never trim draft/action data to make it fit.
    _validate_context(response.context.model_dump(mode="json"), settings)


def _validate_context(context: dict, settings: Settings) -> None:
    if (
        any(len(message["text"]) > settings.security_history_message_max_chars
               for message in context.get("recent_messages", []))
        or len(json.dumps(context, ensure_ascii=False)) > settings.security_context_max_chars
    ):
        _too_large()


def _too_large() -> None:
    raise ServiceError(413, "payload_too_large", "Request too large",
                       "The user text or effective conversation context exceeds its limit.")


class RequestBodyLimits:
    """Bound bytes incrementally, including requests without Content-Length."""

    def __init__(self, app, *, job_max_bytes: int, respond_max_bytes: int,
                 timeout_seconds: float):
        self.app = app
        self.limits = {"/api/job": job_max_bytes, "/api/respond": respond_max_bytes}
        self.timeout_seconds = timeout_seconds

    async def __call__(self, scope, receive, send):
        import asyncio

        from app.api.exception_handlers import problem_response

        limit = self.limits.get(scope.get("path"))
        if scope["type"] != "http" or scope.get("method") != "POST" or limit is None:
            return await self.app(scope, receive, send)

        async def reject(status, code, detail):
            await problem_response(status, code, "Invalid request", detail,
                                   scope["path"])(scope, receive, send)

        headers = {key.lower(): value for key, value in scope["headers"]}
        if b"content-length" in headers:
            try:
                length = int(headers[b"content-length"])
                if length < 0:
                    raise ValueError
            except ValueError:
                return await reject(400, "invalid_request", "Invalid Content-Length.")
            if length > limit:
                return await reject(413, "payload_too_large", "Request body exceeds its limit.")
        body = bytearray()
        try:
            async with asyncio.timeout(self.timeout_seconds):
                while True:
                    message = await receive()
                    if message["type"] == "http.disconnect":
                        return
                    chunk = message.get("body", b"")
                    if len(body) + len(chunk) > limit:
                        return await reject(413, "payload_too_large", "Request body exceeds its limit.")
                    body.extend(chunk)
                    if not message.get("more_body", False):
                        break
        except TimeoutError:
            return await reject(408, "request_timeout", "Request body deadline exceeded.")
        delivered = False

        async def replay():
            nonlocal delivered
            if not delivered:
                delivered = True
                return {"type": "http.request", "body": bytes(body), "more_body": False}
            return await receive()

        return await self.app(scope, replay, send)
