# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod tidy && go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/photo-api ./cmd/server

FROM debian:bookworm-slim AS geonames

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    unzip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /geonames
RUN curl -fsSL https://download.geonames.org/export/dump/cities1000.zip -o cities1000.zip \
    && unzip -q cities1000.zip \
    && rm cities1000.zip \
    && curl -fsSL https://download.geonames.org/export/dump/admin1CodesASCII.txt -o admin1CodesASCII.txt \
    && curl -fsSL https://download.geonames.org/export/dump/countryInfo.txt -o countryInfo.txt

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    libheif-examples \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/photo-api /usr/local/bin/photo-api
COPY --from=geonames /geonames /app/geonames

ENV GOMEMLIMIT=1200MiB

EXPOSE 2283

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS http://127.0.0.1:${PORT:-2283}/api/server/ping || exit 1

ENTRYPOINT ["/usr/local/bin/photo-api"]
