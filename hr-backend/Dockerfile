# 后端镜像：多阶段构建，glebarez/sqlite 为纯 Go 实现，CGO_ENABLED=0 即可。
FROM golang:1.25 AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/server /app/server
COPY configs /app/configs
EXPOSE 8080
ENV CONFIG_PATH=/app/configs/config.yaml
CMD ["/app/server"]
