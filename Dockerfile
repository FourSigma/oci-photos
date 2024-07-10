FROM golang:latest
RUN go install github.com/air-verse/air@latest
COPY go.mod go.sum ./
RUN go mod download
WORKDIR /app
ENTRYPOINT ["air", "-c", ".air.toml"]
