# syntax=docker/dockerfile:1.7
FROM mirror.gcr.io/library/golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/apexx ./cmd/apexx

# Static busybox only for the container healthcheck (wget); distroless has no shell.
FROM mirror.gcr.io/library/busybox:1.36.1-musl AS busybox

# Distroless: no shell, no package manager, runs as non-root (uid 65532).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/apexx /apexx
COPY --from=busybox /bin/busybox /busybox
USER nonroot:nonroot
ENV APEXX_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
  CMD ["/busybox", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/readyz"]
ENTRYPOINT ["/apexx"]
CMD ["serve"]
