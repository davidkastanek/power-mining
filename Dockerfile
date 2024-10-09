FROM golang:1.23.2 as builder

WORKDIR /app

COPY . .

ENV GOOS=linux
ENV GOARCH=amd64

RUN go build -o power-mining .


FROM ubuntu:latest

WORKDIR /app

COPY --from=builder /app/power-mining .

RUN chmod +x power-mining

CMD ["./power-mining"]
