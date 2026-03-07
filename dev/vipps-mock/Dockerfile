FROM golang:1.25-alpine

WORKDIR /app

COPY go.mod ./
COPY main.go ./

RUN go build -o vipps-mock .

EXPOSE 8090

CMD ["./vipps-mock"]
