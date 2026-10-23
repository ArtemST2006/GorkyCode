#!/usr/bin/env python3
"""Fetch only the model revision/files pinned in the AI-service manifest."""

import argparse
from hashlib import sha256
import json
from pathlib import Path
import re
import sys
from urllib.parse import quote
from urllib.request import urlopen
from uuid import uuid4


ROOT = Path(__file__).resolve().parents[1]


def digest_file(path):
    digest = sha256()
    with path.open("rb") as source:
        while block := source.read(1024 * 1024):
            digest.update(block)
    return digest.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--check-only", action="store_true", help="verify without downloading")
    parser.add_argument("--timeout", type=float, default=120)
    args = parser.parse_args()
    if not 0 < args.timeout <= 600:
        parser.error("--timeout must be between 0 and 600 seconds")
    manifest = json.loads((ROOT / "app/embeddings/manifest.json").read_text())
    if not re.fullmatch(r"[0-9a-f]{40}", manifest["revision"]):
        raise ValueError("Model revision must be immutable")
    model = manifest["model"]
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", model):
        raise ValueError("Invalid model identifier")
    for name, expected in manifest["files"].items():
        if Path(name).name != name or not re.fullmatch(r"[0-9a-f]{64}", expected):
            raise ValueError("Invalid manifest artifact")
        destination = args.directory / name
        if destination.is_file() and digest_file(destination) == expected:
            print(f"VERIFIED {name}", flush=True)
            continue
        if args.check_only:
            print(f"FAIL missing or incorrect artifact: {name}", flush=True)
            return 1
        args.directory.mkdir(parents=True, exist_ok=True)
        temporary = args.directory / (name + ".part-" + uuid4().hex)
        url = (f"https://huggingface.co/{model}/resolve/{manifest['revision']}/"
               + quote(name, safe=""))
        print(f"DOWNLOAD {model}@{manifest['revision']} {name}", flush=True)
        try:
            digest = sha256()
            with urlopen(url, timeout=args.timeout) as response, temporary.open("xb") as output:
                while block := response.read(1024 * 1024):
                    output.write(block)
                    digest.update(block)
            if digest.hexdigest() != expected:
                raise ValueError("Downloaded artifact checksum mismatch")
            temporary.replace(destination)
        finally:
            temporary.unlink(missing_ok=True)
    print(f"PASS verified pinned E5 files in {args.directory}", flush=True)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
    except Exception as exc:
        # A redirected Hugging Face URL may contain a signed token; never print it.
        print(f"FAIL model download/verification: {type(exc).__name__}", file=sys.stderr)
        sys.exit(1)
