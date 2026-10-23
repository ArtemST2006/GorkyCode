"""Immutable embedding-space identity, including preprocessing and runtime."""

from dataclasses import dataclass
from hashlib import sha256
import json
from pathlib import Path
import re
from typing import Any

DEFAULT_MANIFEST_PATH = Path(__file__).with_name("manifest.json")


@dataclass(frozen=True)
class EncoderManifest:
    # Store canonical bytes so neither callers nor shallow copies can mutate identity.
    canonical: bytes

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> "EncoderManifest":
        if not re.fullmatch(r"[0-9a-f]{40}", value.get("revision", "")):
            raise ValueError("Manifest must pin a full immutable model revision")
        if value.get("dimensions") != 384:
            raise ValueError("Manifest requires 384 dimensions")
        if not value.get("model") or not value.get("files"):
            raise ValueError("Manifest requires model identity and file hashes")
        for name, digest in value["files"].items():
            if Path(name).name != name or not re.fullmatch(r"[0-9a-f]{64}", digest):
                raise ValueError("Invalid pinned artifact")
        canonical = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
        return cls(canonical.encode("utf-8"))

    @property
    def data(self) -> dict[str, Any]:
        return json.loads(self.canonical)

    @property
    def model(self) -> str:
        return self.data["model"]

    @property
    def model_version(self) -> str:
        return "e5-sha256:" + sha256(self.canonical).hexdigest()

    def verify_files(self, model_path: Path) -> None:
        for name, expected in self.data["files"].items():
            digest = sha256()
            with (model_path / name).open("rb") as source:
                while block := source.read(1024 * 1024):
                    digest.update(block)
            if digest.hexdigest() != expected:
                raise ValueError(f"Pinned encoder artifact checksum mismatch: {name}")


def load_manifest(path: Path = DEFAULT_MANIFEST_PATH) -> EncoderManifest:
    return EncoderManifest.from_dict(json.loads(path.read_text(encoding="utf-8")))
