## Production multi-stage build
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY . .
# Build static binary for small final image
ENV CGO_ENABLED=0
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags='-s -w -extldflags "-static"' -o /out/a-site .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 app
USER app
WORKDIR /app
COPY --from=builder /out/a-site /app/a-site
COPY config.sample.json /app/config.sample.json
ENV LISTEN_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/app/a-site"]

