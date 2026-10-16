FROM golang:1.26-alpine AS build

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /out/backend ./cmd/backend

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/backend /app/backend
COPY config/local.yaml /app/config/local.yaml
ENV CONFIG_PATH=/app/config/local.yaml
EXPOSE 8080
ENTRYPOINT ["/app/backend"]
