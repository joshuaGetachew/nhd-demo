# Stage 1: build. Has the full Go toolchain (~1.3GB), used only to compile.
FROM golang:1.27.3 AS build
WORKDIR /src

# Copy dependency files first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0 builds a static binary that needs no C libraries at runtime.
# -trimpath and -ldflags="-s -w" drop local paths and debug info for a smaller binary.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

# Stage 2: run. Distroless has no shell or package manager, only what the binary needs.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app"]
