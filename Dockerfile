FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ordersapp ./cmd/ordersapp

FROM debian:bookworm-slim

COPY --from=build /out/ordersapp /usr/local/bin/ordersapp

EXPOSE 8080
ENV HTTP_ADDR=:8080
ENTRYPOINT ["ordersapp"]
