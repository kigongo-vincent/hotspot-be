# Build stage
FROM golang:1.26.4-alpine AS builder

WORKDIR /app

# Install git and CA certificates for dependencies
RUN apk add --no-cache git ca-certificates

# Copy Go module files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code and public directory
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -a -installsuffix cgo -o main .

# Final stage
FROM alpine:latest

WORKDIR /app

# Install CA certificates and timezone data
RUN apk --no-cache add ca-certificates tzdata

# Create uploads directory
RUN mkdir -p /app/uploads

# Copy binary
COPY --from=builder /app/main .

# Copy frontend
COPY --from=builder /app/public ./public

EXPOSE 3000

CMD ["./main"]