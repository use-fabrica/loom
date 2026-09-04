# Loom v0 Engine — server image.
#
# Multi-stage: compile the statically-linked binary with the full Go
# toolchain, then ship only the binary on a distroless base so the runtime
# image carries no shell, package manager, or libc to patch.

FROM golang:1.26 AS build
WORKDIR /src

# Cache module downloads separately from source so edits to internal/ don't
# invalidate the (slow) `go mod download` layer.
COPY go.mod go.sum go.work go.work.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /loom ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /loom /loom

# CE_HTTP_ADDR default (see internal/config/config.go); override at runtime.
EXPOSE 8080

ENTRYPOINT ["/loom"]
