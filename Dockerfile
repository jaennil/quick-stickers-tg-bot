FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o bot ./cmd/bot

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tesseract-ocr tesseract-ocr-data-rus tesseract-ocr-data-eng

WORKDIR /app

COPY --from=builder /app/bot .

CMD ["./bot"]
