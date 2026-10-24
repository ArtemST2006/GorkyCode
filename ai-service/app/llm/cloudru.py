"""Cloud.ru Foundation Models through the official asynchronous OpenAI SDK."""

import asyncio
import math
import json
import time

import httpx
from openai import (
    APIConnectionError,
    APIResponseValidationError,
    APIStatusError,
    APITimeoutError,
    AsyncOpenAI,
    DefaultAsyncHttpxClient,
)

from app.core.config import Settings
from app.core.errors import InvalidModelOutput, ServiceError
from app.core.logging import logger
from app.llm.protocols import ChatMessage


class CloudRuGateway:
    def __init__(self, settings: Settings, client: AsyncOpenAI | None = None) -> None:
        self.settings = settings
        self._owns_client = client is None
        self._client = client
        if (
            client is None
            and settings.cloudru_api_key
            and settings.cloudru_api_key.get_secret_value().strip()
        ):
            options = {}
            if settings.cloudru_force_ipv4:
                options["http_client"] = DefaultAsyncHttpxClient(
                    transport=httpx.AsyncHTTPTransport(local_address="0.0.0.0"),
                    follow_redirects=False,
                )
            self._client = AsyncOpenAI(
                api_key=settings.cloudru_api_key.get_secret_value().strip(),
                base_url=settings.cloudru_base_url,
                timeout=settings.cloudru_timeout_seconds,
                # Retries are bounded below; avoid multiplying them inside the SDK.
                max_retries=0,
                **options,
            )

    async def close(self) -> None:
        if self._owns_client and self._client is not None:
            await self._client.close()

    async def complete(self, messages: list[ChatMessage]) -> str:
        key = self.settings.cloudru_api_key
        if self._client is None or key is None or not key.get_secret_value().strip():
            raise ServiceError(
                503,
                "model_not_configured",
                "Model unavailable",
                "CLOUDRU_API_KEY or API_KEY is not configured.",
            )
        for attempt in range(self.settings.cloudru_max_retries + 1):
            retry_after = None
            started = time.monotonic()
            try:
                completion = await self._client.chat.completions.create(
                    model=self.settings.cloudru_model,
                    messages=messages,
                    max_tokens=self.settings.cloudru_max_tokens,
                    temperature=0.5,
                    presence_penalty=0,
                    top_p=0.95,
                    timeout=self.settings.cloudru_timeout_seconds,
                )
            except (APIResponseValidationError, json.JSONDecodeError):
                raise InvalidModelOutput(
                    "The provider returned an invalid completion envelope."
                ) from None
            except APITimeoutError as exc:
                # Class names and timings only: never log provider text or credentials.
                logger.warning(
                    "cloudru_request_timeout cause=%s elapsed_ms=%d timeout_seconds=%s attempt=%d",
                    type(exc.__cause__).__name__,
                    int((time.monotonic() - started) * 1000),
                    self.settings.cloudru_timeout_seconds,
                    attempt + 1,
                )
                error = ServiceError(
                    504,
                    "model_timeout",
                    "Model timeout",
                    "The model did not respond within the configured timeout.",
                )
                retry = True
            except APIConnectionError as exc:
                logger.warning(
                    "cloudru_connection_failed cause=%s elapsed_ms=%d attempt=%d",
                    type(exc.__cause__).__name__,
                    int((time.monotonic() - started) * 1000),
                    attempt + 1,
                )
                error = ServiceError(
                    503,
                    "model_unavailable",
                    "Model unavailable",
                    "The model provider could not be reached.",
                )
                retry = True
            except APIStatusError as exc:
                error, retry = self._status_error(exc.status_code)
                retry_after = self._retry_after(exc.response)
                # Only status and numeric delay; never provider bodies or headers
                # containing user text, credentials or signed URLs.
                logger.warning(
                    "cloudru_request_failed upstream_status=%s retry_after_seconds=%s",
                    exc.status_code,
                    retry_after,
                )
            else:
                return self._content(completion)
            if not retry or attempt >= self.settings.cloudru_max_retries:
                raise error from None
            # A long quota reset must be handled by the caller, not consume the
            # whole job deadline or produce another premature paid request.
            if retry_after is not None and retry_after > 5:
                raise error from None
            await asyncio.sleep(max(0.25 * 2**attempt, retry_after or 0))
        raise RuntimeError("Unreachable model retry state")

    @staticmethod
    def _retry_after(response: httpx.Response) -> float | None:
        try:
            value = float(response.headers["retry-after"])
            return value if math.isfinite(value) and value >= 0 else None
        except (KeyError, ValueError):
            return None

    @staticmethod
    def _status_error(status: int) -> tuple[ServiceError, bool]:
        if status in (401, 403):
            code, detail = (
                "model_authentication_failed",
                "The model provider rejected the service credentials or permissions.",
            )
        elif status == 404:
            code, detail = (
                "model_not_available",
                "The configured model is unavailable for this account.",
            )
        elif status == 429:
            code, detail = (
                "model_rate_limited",
                "The model provider is temporarily rate limiting requests.",
            )
        elif status >= 500:
            code, detail = "model_unavailable", "The model provider returned a temporary error."
        else:
            code, detail = (
                "model_request_rejected",
                "The model provider rejected the configured request.",
            )
        return ServiceError(503, code, "Model unavailable", detail), status == 429 or status >= 500

    @staticmethod
    def _content(completion: object) -> str:
        choices = getattr(completion, "choices", None)
        if not isinstance(choices, list) or len(choices) != 1:
            raise InvalidModelOutput("The model returned an invalid completion envelope.")
        choice = choices[0]
        message = getattr(choice, "message", None)
        if getattr(choice, "finish_reason", None) != "stop" or message is None:
            raise InvalidModelOutput("The model did not finish a complete response.")
        content = getattr(message, "content", None)
        if not isinstance(content, str) or not content.strip() or getattr(message, "refusal", None):
            raise InvalidModelOutput("The model returned no final content.")
        return content
