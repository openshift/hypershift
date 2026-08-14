FROM docker.io/golang:latest
WORKDIR /cleanroles
COPY . .
RUN go build .
ENTRYPOINT /cleanroles/cleanroles
