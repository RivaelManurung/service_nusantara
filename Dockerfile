# --- build -------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Let Go fetch whatever toolchain go.mod asks for. Pinning only the base image
# means a `go mod tidy` that bumps the go directive breaks the build with
# "go.mod requires go >= X" long after the change that caused it.
ENV GOTOOLCHAIN=auto

# Copy the manifests first so dependency download is cached independently of
# source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# --- runtime -----------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/api /app/api

# Run unprivileged: a compromised process should not be root in the container.
USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/app/api"]
