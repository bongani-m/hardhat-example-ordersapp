FROM node:22-bookworm AS ui

WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -o /out/ordersapp ./cmd/ordersapp

FROM debian:bookworm-slim

COPY --from=build /out/ordersapp /usr/local/bin/ordersapp

EXPOSE 8080
ENV HTTP_ADDR=:8080
ENTRYPOINT ["ordersapp"]
