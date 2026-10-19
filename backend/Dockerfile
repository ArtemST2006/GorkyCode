FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -buildvcs=false -trimpath -o /out/backend ./cmd/backend

# забираем .so библиотеки sherpa и onnxruntime из module cache
RUN mkdir -p /out/lib && \
    SHERPA_DIR="$(go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-linux)" && \
    SHERPA_LIB_DIR="$SHERPA_DIR/lib/$(uname -m)-unknown-linux-gnu" && \
    test -d "$SHERPA_LIB_DIR" && \
    find "$SHERPA_LIB_DIR" -maxdepth 1 -name '*.so*' -exec cp -L {} /out/lib/ \; && \
    chmod 755 /out/lib/*.so*

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates wget bzip2 \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -r -u 10001 app && mkdir -p /app/model && chown -R app:app /app

WORKDIR /app
COPY --from=build /out/backend /app/backend
COPY --from=build /out/lib/ /usr/local/lib/
RUN ldconfig

COPY config/local.yaml /app/config/local.yaml
COPY scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

ENV CONFIG_PATH=/app/config/local.yaml \
    MODEL_PATH=/app/model/asr-model

USER app
EXPOSE 8080
ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["/app/backend"]
