FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /clubdue ./cmd/clubdue

FROM scratch
COPY --from=builder /clubdue /clubdue
EXPOSE 8080
ENV PORT=8080
ENTRYPOINT ["/clubdue"]
