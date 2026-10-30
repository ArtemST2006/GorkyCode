"""Request timing and safe fallback for unexpected request failures."""

import asyncio

from collections.abc import Awaitable, Callable
from time import perf_counter

from fastapi import Request, Response

from app.api.exception_handlers import problem_response
from app.core.logging import logger


async def observe_requests(
    request: Request, call_next: Callable[[Request], Awaitable[Response]]
) -> Response:
    started = perf_counter()
    try:
        response = await call_next(request)
    except Exception:
        # Exception strings/tracebacks may contain provider bodies and user text.
        response = problem_response(
            500,
            "internal_error",
            "Internal server error",
            "An unexpected internal error occurred.",
            request.url.path,
        )
        logger.error("request_unhandled_error")
    route = getattr(request.scope.get("route"), "path", "unmatched")
    if route == "unmatched":
        # FastAPI's built-in documentation uses Starlette routes, which do not
        # populate scope['route']. Recognize only configured static URLs; never
        # log arbitrary request paths that may contain user-provided text.
        documentation_paths = {
            getattr(request.app, name, None)
            for name in ("openapi_url", "docs_url", "redoc_url", "swagger_ui_oauth2_redirect_url")
        }
        if request.url.path in documentation_paths:
            route = request.url.path
    logger.info(
        "request method=%s route=%s status=%s duration_ms=%.1f",
        request.method,
        route,
        response.status_code,
        (perf_counter() - started) * 1000,
    )
    response.headers["Cache-Control"] = "no-store"
    response.headers["X-Content-Type-Options"] = "nosniff"
    return response


class EmbedRequestMiddleware:
    """Bound raw JSON bytes before FastAPI buffers or parses a request body."""

    MAX_BYTES = 16384

    def __init__(self, app, timeout: float = 10.0):
        self.app = app
        self.timeout = timeout

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http" or scope["path"] != "/api/embed" or scope["method"] != "POST":
            return await self.app(scope, receive, send)
        response_started = False

        async def guarded_send(message):
            nonlocal response_started
            if message["type"] == "http.response.start":
                response_started = True
            await send(message)

        try:
            async with asyncio.timeout(self.timeout):
                await self._handle(scope, receive, guarded_send)
        except TimeoutError:
            # Once headers have been emitted HTTP status cannot be changed.
            # Let the server terminate the incomplete response instead of emitting
            # a second response.start and corrupting the ASGI exchange.
            if response_started:
                raise
            await self._problem(scope, receive, send, 504, "embedding_timeout",
                                "The embedding deadline was exceeded.")

    async def _handle(self, scope, receive, send):
        headers = {key.lower(): value for key, value in scope["headers"]}
        media = headers.get(b"content-type", b"").split(b";", 1)[0].strip().lower()
        if media != b"application/json":
            return await self._problem(scope, receive, send, 415,
                                       "unsupported_media_type", "Expected application/json.")
        length = headers.get(b"content-length")
        if length is not None:
            try:
                declared = int(length)
                if declared < 0:
                    raise ValueError
            except ValueError:
                return await self._problem(scope, receive, send, 400,
                                           "invalid_request", "Invalid Content-Length.")
            if declared > self.MAX_BYTES:
                return await self._problem(scope, receive, send, 413,
                                           "payload_too_large", "Request exceeds 16384 bytes.")
        body = bytearray()
        while True:
            message = await receive()
            if message["type"] == "http.disconnect":
                return
            chunk = message.get("body", b"")
            if len(body) + len(chunk) > self.MAX_BYTES:
                return await self._problem(scope, receive, send, 413,
                                           "payload_too_large", "Request exceeds 16384 bytes.")
            body.extend(chunk)
            if not message.get("more_body", False):
                break
        delivered = False

        async def replay():
            nonlocal delivered
            if not delivered:
                delivered = True
                return {"type": "http.request", "body": bytes(body), "more_body": False}
            return await receive()

        return await self.app(scope, replay, send)

    async def _problem(self, scope, receive, send, status, code, detail):
        response = problem_response(status, code, "Invalid embedding request", detail,
                                    scope["path"])
        await response(scope, receive, send)
