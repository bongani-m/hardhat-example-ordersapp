# hardhat-example-ordersapp

A small orders API. [HardhatDB](https://github.com/bongani-m/hardhatdb) stores each order. [HardhatKV](https://github.com/bongani-m/hardhatkv) caches the JSON. [HardhatQ](https://github.com/bongani-m/hardhatq) carries an “order created” message, and this process consumes it and sets the order to `done`.

Each database runs as one container. HardhatDB has no Raft address, so cluster mode stays off. HardhatKV uses the image default, which does not enable cluster mode. HardhatQ is one broker.

The MySQL password `secret` is the local bootstrap password from the HardhatDB container example. It is not a production secret.

HardhatDB creates that account with `caching_sha2_password`. The published server accepts that login on TLS. `certs/` is a local development certificate for the hostname `hardhatdb`. It is not a production CA.

## Run

HardhatKV and HardhatQ have no `latest` tag on GHCR. Compose pins the published tags: HardhatDB `v0.1.0-alpha.3`, HardhatKV `v1.3.0-alpha.1`, HardhatQ `v0.11.0-alpha.1`.

```bash
docker compose up --build
```

The app listens on port 8080. It retries each database for up to two minutes.

```bash
curl -s localhost:8080/up
curl -s -X POST localhost:8080/orders \
  -H 'content-type: application/json' \
  -d '{"item":"notebook"}'
curl -s localhost:8080/orders/1
```

`GET /orders/1` reads HardhatKV first. Right after create, `status` may still be `new`. A moment later the consumer has set it to `done` and refreshed the cache.

`POST /orders` returns 201 with the new order. `GET /up` returns 200 when all three databases answer, and 503 otherwise. Kamal's proxy checks that path by default.

Published ports: HardhatDB `3306`, HardhatKV `6399`, HardhatQ `5672`.

## Control plane

A Kamal deploy runs this process only. HardhatDB, HardhatKV, and HardhatQ are clusters created in the control plane. The control plane user is `root`. Put this server's public address on each cluster's client allowlist.

`MYSQL_ADDR` is a HardhatDB node's public IP and port, such as `203.0.113.10:3306`. `KV_ADDR` is `203.0.113.11:6399`. `AMQP_URL` is `amqps://root:<password>@203.0.113.12:5672`. The server certificate names that public IP, so the host has to be the IP. `MYSQL_TLS_CA` or `MYSQL_TLS_CA_PEM` is the HardhatDB CA. `AMQP_TLS_CA` or `AMQP_TLS_CA_PEM` is the HardhatQ CA, and an `amqps` URL requires one of them. `KV_PASSWORD` is the Redis AUTH password. An empty password skips AUTH, which is what the local HardhatKV container does.

```bash
mysql --host=127.0.0.1 --port=3306 --user=root --password=secret \
  --ssl-mode=VERIFY_CA --ssl-ca=certs/ca.crt shop \
  --execute="SELECT id, item, status FROM orders;"
```
