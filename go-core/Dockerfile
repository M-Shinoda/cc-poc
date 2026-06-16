FROM golang:1.22-alpine AS builder

ENV GOFLAGS=-mod=mod

WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /cc-poc ./cmd/main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /cc-poc /cc-poc
ENTRYPOINT ["/cc-poc"]
