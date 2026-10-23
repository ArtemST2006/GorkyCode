"""Provider-independent boundary and CPU E5 implementation."""

from dataclasses import dataclass
from importlib.metadata import version
import math
from pathlib import Path
import struct
from typing import Literal, Protocol, Sequence
import unicodedata

from .manifest import EncoderManifest, load_manifest

Purpose = Literal["document", "query"]
DIMENSIONS = 384
NORM_TOLERANCE = 1e-3


class InvalidEmbedding(ValueError):
    """Output is unsafe or incompatible with the canonical vector contract."""


def validate_vector(values: Sequence[float]) -> tuple[float, ...]:
    if len(values) != DIMENSIONS:
        raise InvalidEmbedding("Expected exactly 384 coordinates")
    converted = []
    for value in values:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise InvalidEmbedding("Coordinates must be finite float32-compatible numbers")
        try:
            number = struct.unpack("f", struct.pack("f", value))[0]
        except (OverflowError, struct.error):
            raise InvalidEmbedding("Coordinate is outside float32 range") from None
        if not math.isfinite(number):
            raise InvalidEmbedding("Coordinates must be finite")
        converted.append(number)
    norm = math.sqrt(math.fsum(number * number for number in converted))
    if norm == 0 or abs(norm - 1.0) > NORM_TOLERANCE:
        raise InvalidEmbedding("Expected a nonzero L2-normalized vector")
    return tuple(converted)


@dataclass(frozen=True)
class EmbeddingResult:
    embedding: tuple[float, ...]
    model: str
    model_version: str

    def __post_init__(self) -> None:
        object.__setattr__(self, "embedding", validate_vector(self.embedding))
        if not self.model.strip() or not self.model_version.strip():
            raise InvalidEmbedding("Model identity must be nonempty")

    def to_dict(self) -> dict:
        return {"embedding": list(self.embedding), "model": self.model,
                "model_version": self.model_version}


class Encoder(Protocol):
    def encode(self, purpose: Purpose, text: str) -> EmbeddingResult: ...


def prepare_text(purpose: Purpose, text: str, manifest: EncoderManifest) -> str:
    if purpose not in ("document", "query"):
        raise ValueError("Unsupported embedding purpose")
    normalized = unicodedata.normalize("NFC", text).strip()
    if not normalized:
        raise ValueError("Embedding text must be nonblank")
    return manifest.data["prefixes"][purpose] + normalized


class E5Encoder:
    """Loads verified local safetensors once. Call from a bounded worker pool.

    The input is right-truncated to 512 tokens including prefix/special tokens.
    Mean pooling includes all non-padding tokens, following the model card.
    """

    def __init__(self, model_path: str | Path, manifest: EncoderManifest | None = None,
                 *, num_threads: int = 2) -> None:
        self.manifest = manifest or load_manifest()
        spec = self.manifest.data
        supported = {
            "unicode": "NFC", "strip": True, "preserve_case": True,
            "preserve_internal_whitespace": True,
        }
        if (spec.get("preprocessing") != supported
                or spec.get("pooling") != "attention-mask-mean"
                or spec.get("normalization") != "L2-float32"
                or spec.get("max_tokens") != 512
                or spec.get("truncation") != "right-including-prefix-and-special-tokens"
                or spec.get("prefixes") != {"document": "passage: ", "query": "query: "}):
            raise ValueError("Unsupported encoder manifest policy")
        for package, expected in spec["runtime"].items():
            if version(package) != expected:
                raise ValueError(f"Pinned encoder runtime mismatch: {package}")
        self.manifest.verify_files(Path(model_path))
        # Heavy dependencies intentionally lazy: fake-based tests and /api/job need none.
        import torch
        from transformers import AutoModel, AutoTokenizer

        if num_threads < 1:
            raise ValueError("CPU thread count must be positive")
        torch.set_num_threads(num_threads)
        self._torch = torch
        self._tokenizer = AutoTokenizer.from_pretrained(
            str(model_path), local_files_only=True, trust_remote_code=False, use_fast=True,
        )
        self._tokenizer.truncation_side = "right"
        self._model = AutoModel.from_pretrained(
            str(model_path), local_files_only=True, trust_remote_code=False,
            use_safetensors=True, dtype=torch.float32,
        ).to("cpu").eval()

    @property
    def model(self) -> str:
        return self.manifest.model

    @property
    def model_version(self) -> str:
        return self.manifest.model_version

    def encode(self, purpose: Purpose, text: str) -> EmbeddingResult:
        prepared = prepare_text(purpose, text, self.manifest)
        tokens = self._tokenizer(
            [prepared], padding=True, truncation=True, max_length=512, return_tensors="pt",
        )
        torch = self._torch
        with torch.inference_mode():
            hidden = self._model(**tokens).last_hidden_state
            mask = tokens["attention_mask"].unsqueeze(-1).to(hidden.dtype)
            pooled = (hidden * mask).sum(dim=1) / mask.sum(dim=1).clamp(min=1)
            normalized = torch.nn.functional.normalize(pooled.float(), p=2, dim=1)
        return EmbeddingResult(tuple(normalized[0].tolist()), self.model, self.model_version)
