# Copyright (C) 2026 The everest-mcp Contributors
# Licensed under the Apache License, Version 2.0.

# ---- build ----
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/everest-mcp ./cmd/everest-mcp

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/everest-mcp /usr/local/bin/everest-mcp
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/everest-mcp"]
