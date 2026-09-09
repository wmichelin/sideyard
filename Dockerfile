FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /clubdue ./cmd/clubdue
RUN mkdir -m 1777 /runtime-tmp

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /clubdue /clubdue
# net/http spools larger multipart files here, then removes them after requests.
COPY --from=builder /runtime-tmp /tmp
EXPOSE 8080
ENV PORT=8080
ENTRYPOINT ["/clubdue"]
