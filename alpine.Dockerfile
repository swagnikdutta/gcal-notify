# Stage 1
FROM golang:1.24-alpine as builder
RUN apk add --no-cache git
WORKDIR /gcal-notify
RUN git clone https://github.com/swagnikdutta/gcal-notify.git .
RUN CGO_ENABLED=0 GOOS=linux go build -o gcal-notify-bin .

# Stage 2
FROM alpine:latest
WORKDIR /gcal-notify
COPY --from=builder /gcal-notify/gcal-notify-bin .
CMD ["./gcal-notify-bin"]
