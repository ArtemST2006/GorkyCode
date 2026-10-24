"""Local deterministic embedding boundary; no network or LLM calls at inference."""

from .encoder import EmbeddingResult, Encoder, E5Encoder, InvalidEmbedding
from .manifest import EncoderManifest, load_manifest

__all__ = ["EmbeddingResult", "Encoder", "E5Encoder", "InvalidEmbedding", "EncoderManifest", "load_manifest"]
