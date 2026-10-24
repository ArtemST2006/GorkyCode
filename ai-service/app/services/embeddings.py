"""A process-local encoder with bounded admission and cancellation-safe capacity."""

import asyncio
from concurrent.futures import Future, ThreadPoolExecutor

from app.core.errors import ServiceError
from app.embeddings import Encoder
from app.schemas.embedding import EmbeddingRequest, EmbeddingResponse


def unavailable() -> ServiceError:
    return ServiceError(503, "embedding_unavailable", "Embedding unavailable",
                        "The embedding encoder is unavailable or busy.")


class EmbeddingService:
    def __init__(self, encoder: Encoder | None, *, max_workers: int, timeout: float):
        self.encoder = encoder
        self.timeout = timeout
        self.max_workers = max_workers
        self._executor = ThreadPoolExecutor(max_workers=max_workers,
                                            thread_name_prefix="embedding")
        self._inflight: set[Future] = set()
        self._closed = False

    async def process(self, payload: EmbeddingRequest) -> EmbeddingResponse:
        # All admission runs on the owning event loop, without an await between
        # checking capacity and registering the actual concurrent future.
        if self._closed or self.encoder is None or len(self._inflight) >= self.max_workers:
            raise unavailable()
        loop = asyncio.get_running_loop()
        future = self._executor.submit(self.encoder.encode, payload.purpose, payload.text)
        self._inflight.add(future)

        def completed(done: Future) -> None:
            # A cancelled HTTP task must never release a CPU worker early.
            try:
                loop.call_soon_threadsafe(self._inflight.discard, done)
            except RuntimeError:
                # Event loop can close between native worker completion and
                # scheduling its callback during process shutdown.
                pass

        future.add_done_callback(completed)
        wrapped = asyncio.wrap_future(future)
        # Consume late exceptions after HTTP cancellation/timeout without logging
        # exception details (which could contain submitted text).
        wrapped.add_done_callback(lambda done: None if done.cancelled() else done.exception())
        try:
            result = await asyncio.wait_for(asyncio.shield(wrapped), self.timeout)
            return EmbeddingResponse.model_validate(result.to_dict())
        except TimeoutError:
            raise ServiceError(504, "embedding_timeout", "Embedding timeout",
                               "The embedding deadline was exceeded.") from None
        except asyncio.CancelledError:
            raise
        except Exception:
            raise ServiceError(500, "embedding_failed", "Embedding failed",
                               "An unexpected embedding error occurred.") from None

    def close(self) -> None:
        self._closed = True
        # Running native CPU operations cannot be interrupted; no queued jobs exist.
        self._executor.shutdown(wait=False, cancel_futures=True)
