#!/bin/sh
set -eu

MODEL_DIR="${MODEL_PATH:-/app/model/asr-model}"
MODEL_URL="${MODEL_URL:-https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-zipformer-ru-2024-09-18.tar.bz2}"

if [ ! -f "$MODEL_DIR/tokens.txt" ]; then
  echo "model not found, downloading from GitHub: $MODEL_URL"

  TMP="$(mktemp -d)"
  wget --tries=3 -O "$TMP/model.tar.bz2" "$MODEL_URL"
  mkdir -p "$TMP/out"
  tar -xjf "$TMP/model.tar.bz2" -C "$TMP/out" --strip-components=1

  rm -rf "$MODEL_DIR"
  mkdir -p "$(dirname "$MODEL_DIR")"
  mv "$TMP/out" "$MODEL_DIR"
  rm -rf "$TMP"
  echo "model ready"
fi

exec "$@"
