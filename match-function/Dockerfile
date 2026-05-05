FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o match-function .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/match-function .
EXPOSE 50502
CMD ["./match-function"]
