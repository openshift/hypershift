FROM docker.io/golang:latest
WORKDIR /cleanzones
COPY . .
RUN go build .
ENTRYPOINT /cleanzones/cleanzones
