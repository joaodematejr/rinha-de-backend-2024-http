
FROM golang:1.22.0-alpine3.19 AS builder


WORKDIR /app


COPY . .

RUN go mod download


RUN go build -o main .

FROM alpine:3.19


WORKDIR /app

COPY --from=builder /app/main .

EXPOSE 8080

ENV TZ=America/Sao_Paulo

RUN apk add --no-cache tzdata

CMD ["./main"]
