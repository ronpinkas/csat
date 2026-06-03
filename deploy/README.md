# Deploying CSAT

A deployment is one binary, one config file, one secrets file, and a data directory.

## 1. Files on the host

```
/usr/local/bin/csat            # the binary (dist/csat-linux-amd64)
/etc/csat/config.toml          # structural config            (chmod 600)
/etc/csat/csat.env             # secrets (CSAT_CRYPTO_SECRET…) (chmod 600)
/var/lib/csat/                 # csat.db, crypto.key, autocert cache (owned by csat)
```

## 2. Setup

```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin csat
sudo install -m 0755 dist/csat-linux-amd64 /usr/local/bin/csat
sudo mkdir -p /etc/csat /var/lib/csat
sudo chown csat:csat /var/lib/csat && sudo chmod 700 /var/lib/csat

sudo cp config.example.toml /etc/csat/config.toml      # edit site.name, db.path, timezone
sudo cp .env.example        /etc/csat/csat.env         # set CSAT_ADMIN_INITIAL_PW
sudo chmod 600 /etc/csat/config.toml /etc/csat/csat.env

sudo cp deploy/csat.service /etc/systemd/system/csat.service
sudo systemctl daemon-reload
sudo systemctl enable --now csat
```

On a **platform-provisioned** bundle these files (including a personalized `csat.env` with the
per-customer `CSAT_CRYPTO_SECRET`) are already populated — just install and `enable --now`.

First boot migrates the DB and seeds the admin user. Watch the log for the generated token
secret if you didn't pin one:

```sh
journalctl -u csat -f
```

## 3. TLS / reverse proxy

**Option A — behind your existing proxy (recommended).** Keep `server.tls.mode = "off"`, leave
the app on `:8080`, and have nginx/Caddy/ALB terminate TLS and forward to it. Ensure the proxy
sets `X-Forwarded-For`/`X-Forwarded-Proto`, and that `server.trust_proxy = true` with the
proxy's address in `server.trusted_proxies`.

**Option B — built-in autocert.** Set `server.tls.mode = "autocert"`, list your `domains`, point
the DNS A record at the host, open ports 80 and 443, and uncomment
`AmbientCapabilities=CAP_NET_BIND_SERVICE` in the unit so the service may bind low ports.

## 4. Verify

```sh
curl -fsS http://127.0.0.1:8080/healthz        # -> ok
```

Then open `/login`, sign in with the bootstrap admin, complete the forced password change, and
copy the token secret from `/settings` to your call platform's link-builder.

## Backups

Back up `/var/lib/csat/csat.db` (SQLite, WAL mode). A consistent copy:

```sh
sqlite3 /var/lib/csat/csat.db ".backup '/var/backups/csat-$(date +%F).db'"
```
