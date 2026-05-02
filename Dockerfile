# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o playmore .

# Run stage
FROM alpine:3.19
RUN apk --no-cache add ca-certificates && \
    adduser -D -u 1000 playmore && \
    mkdir -p /app/data && \
    chown -R playmore:playmore /app
WORKDIR /app
COPY --from=builder --chown=playmore:playmore /app/playmore .
USER playmore
EXPOSE 8080
VOLUME ["/app/data"]
ENTRYPOINT ["./playmore"]
CMD ["--port", "8080", "--data", "/app/data"]
