FROM golang:1.26-alpine AS build

WORKDIR /src
RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN GOSUMDB=off go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp ./cmd/mcp \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcpkey ./cmd/mcpkey

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -g 10001 -S app \
 && adduser -u 10001 -S -G app app

WORKDIR /app
COPY --from=build /out/api /out/worker /out/mcp /out/mcpkey /app/
USER app

EXPOSE 8080
CMD ["/app/api"]
