FROM python:3.12.14-slim-bookworm@sha256:392307d22300de8b5986851a12d9176dfc0fc073e65bf6523ebd7dcbeb23564e AS build

ARG PIP_INDEX_URL=https://pypi-mirror.gitverse.ru/simple/
ARG TORCH_INDEX_URL=https://download.pytorch.org/whl/cpu
ENV PIP_INDEX_URL=${PIP_INDEX_URL}
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /build
COPY pyproject.toml requirements.lock README.md ./
COPY app ./app
COPY scripts/download_embedding_model.py ./scripts/download_embedding_model.py
RUN python -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir --no-deps \
       --index-url "${TORCH_INDEX_URL}" 'torch==2.10.0+cpu' \
    && /opt/venv/bin/pip install --no-cache-dir \
       --index-url "${PIP_INDEX_URL}" \
       -c requirements.lock . \
    && /opt/venv/bin/pip check \
    && /opt/venv/bin/python scripts/download_embedding_model.py /opt/embedding-model

FROM python:3.12.14-slim-bookworm@sha256:392307d22300de8b5986851a12d9176dfc0fc073e65bf6523ebd7dcbeb23564e AS runtime

ENV PATH="/opt/venv/bin:$PATH" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PORT=8090 \
    EMBEDDING_ENABLED=true \
    EMBEDDING_MODEL_PATH=/opt/embedding-model \
    EMBEDDING_NUM_THREADS=2 \
    EMBEDDING_MAX_WORKERS=1 \
    EMBEDDING_TIMEOUT_SECONDS=10 \
    HF_HUB_OFFLINE=1 \
    TRANSFORMERS_OFFLINE=1 \
    HF_HOME=/tmp/huggingface \
    TOKENIZERS_PARALLELISM=false

RUN groupadd --gid 10001 app \
    && useradd --uid 10001 --gid app --no-create-home app
WORKDIR /service
COPY --from=build /opt/venv /opt/venv
COPY --from=build /opt/embedding-model /opt/embedding-model
COPY server.py ./
COPY scripts ./scripts
USER 10001:10001
EXPOSE 8090
HEALTHCHECK --interval=5s --timeout=3s --start-period=30s --retries=5 \
    CMD python -c "import os,urllib.request; urllib.request.urlopen('http://127.0.0.1:'+os.environ.get('PORT','8090')+'/health',timeout=2)"
CMD ["python", "server.py"]
