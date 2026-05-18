# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o main .

# Run stage
FROM alpine:latest
RUN apk add --no-cache ffmpeg ca-certificates
WORKDIR /app
COPY --from=builder /app/main .
COPY --from=builder /app/.env .
# Ensure tmp and storage dirs exist
RUN mkdir -p tmp/streams storage/archives

EXPOSE 8080
CMD ["./main"]
