FROM golang:1.27-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o pc-principal ./cmd/pc-principal/

FROM alpine:3.24
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/pc-principal .
EXPOSE 8080
CMD ["./pc-principal"]
