FROM golang:1.25-alpine AS builder

# The service does not depend on the other modules of the workspace, so it is built on its own.
ENV GOWORK=off
WORKDIR /app/services/UserService

COPY services/UserService/go.mod services/UserService/go.sum ./
RUN go mod download

COPY services/UserService/ ./
RUN CGO_ENABLED=0 go build -o /user_service-app ./cmd

FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata
WORKDIR /root/
COPY --from=builder /user_service-app .
CMD ["./user_service-app"]
