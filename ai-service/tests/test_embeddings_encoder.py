"""Offline encoder checks: no download and no LLM calls."""

from dataclasses import replace
import socket

import pytest

from app.embeddings import EmbeddingResult, EncoderManifest, InvalidEmbedding, load_manifest
from app.embeddings.encoder import prepare_text


@pytest.fixture(autouse=True)
def no_network(monkeypatch):
    def forbidden(*args, **kwargs):
        raise AssertionError("Offline encoder test attempted a network call")
    monkeypatch.setattr(socket, "create_connection", forbidden)
    monkeypatch.setattr(socket.socket, "connect", forbidden)


@pytest.mark.parametrize("purpose,prefix", [("query", "query: "), ("document", "passage: ")])
def test_preparation(purpose, prefix):
    value = prepare_text(purpose, " \tИ\u0306ван  ABC\nвстреча \n", load_manifest())
    assert value == prefix + "Йван  ABC\nвстреча"


@pytest.mark.parametrize("purpose,text", [("query", " \n"), ("bad", "valid")])
def test_reject_invalid_text_or_purpose(purpose, text):
    with pytest.raises(ValueError):
        prepare_text(purpose, text, load_manifest())


def test_identity_stable_after_key_reordering_and_immutable():
    manifest = load_manifest()
    assert manifest.model_version == EncoderManifest.from_dict(
        dict(reversed(list(manifest.data.items())))
    ).model_version
    copy = manifest.data
    copy["prefixes"]["query"] = "wrong: "
    assert manifest.data["prefixes"]["query"] == "query: "


@pytest.mark.parametrize("key,value", [
    ("revision", "a" * 40), ("pooling", "cls"), ("normalization", "none"),
    ("max_tokens", 256), ("truncation", "left"),
    ("prefixes", {"query": "q: ", "document": "p: "}),
    ("preprocessing", {"unicode": "NFKC"}),
    ("runtime", {"torch": "another-version"}),
    ("files", {"model.safetensors": "b" * 64}),
])
def test_changed_space_changes_version(key, value):
    original = load_manifest()
    data = original.data
    data[key] = value
    assert EncoderManifest.from_dict(data).model_version != original.model_version


def test_manifest_requires_revision_and_safe_files():
    for key, value in [("revision", "main"), ("dimensions", 768),
                       ("files", {"../escape": "a" * 64})]:
        data = load_manifest().data
        data[key] = value
        with pytest.raises(ValueError):
            EncoderManifest.from_dict(data)


def test_file_integrity_rejects_missing_or_mutated_artifact(tmp_path):
    from hashlib import sha256
    data = load_manifest().data
    data["files"] = {"config.json": sha256(b"expected").hexdigest()}
    manifest = EncoderManifest.from_dict(data)
    with pytest.raises(FileNotFoundError):
        manifest.verify_files(tmp_path)
    (tmp_path / "config.json").write_bytes(b"tampered")
    with pytest.raises(ValueError, match="checksum"):
        manifest.verify_files(tmp_path)
    (tmp_path / "config.json").write_bytes(b"expected")
    manifest.verify_files(tmp_path)


def unit_vector():
    return [1.0] + [0.0] * 383


@pytest.mark.parametrize("values", [
    [], [1.0] * 383, [0.0] * 384, [1.0] * 384,
    [True] + [0.0] * 383, ["1"] + [0.0] * 383,
    [float("nan")] + [0.0] * 383,
    [float("inf")] + [0.0] * 383,
    [1e100] + [0.0] * 383,
])
def test_invalid_vectors(values):
    with pytest.raises(InvalidEmbedding):
        EmbeddingResult(tuple(values), "model", "version")


def test_result_wire_has_only_canonical_fields():
    result = EmbeddingResult(tuple(unit_vector()), "model", "version")
    assert result.to_dict() == {
        "embedding": unit_vector(), "model": "model", "model_version": "version",
    }
    with pytest.raises(InvalidEmbedding):
        replace(result, model_version=" ")
