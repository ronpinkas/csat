# Install (concise)

The deliverable is a **single static Linux binary** (everything embedded — templates, assets,
Chart.js, migrations; no runtime, no shared libraries). You only ship that file plus a config.

## Option A — release package (fastest)
Build a self-contained tarball, copy it to the host, and run the installer:

```sh
make package                                   # -> dist/csat-<version>-linux-amd64.tar.gz
scp dist/csat-*-linux-amd64.tar.gz host:/tmp/
ssh host
tar xzf /tmp/csat-*-linux-amd64.tar.gz && cd csat-*-linux-amd64
sudo ./install.sh                              # creates user/dirs, installs binary + config + unit
# edit /etc/csat/config.toml and /etc/csat/csat.env, then:
sudo systemctl enable --now csat
```

The tarball contains: `csat` (binary), `config.example.toml`, `.env.example`, `csat.service`,
`install.sh`, `INSTALL.md`, `README.md`.

> Minimal case: the binary even runs with **no config file** (built-in defaults, auto-generated
> token secret, auto-created DB) — so `./csat` alone works for a quick trial; add a `config.toml`
> for real deployments.

## Option B — manual

## 1. Build
Requires Go 1.22+ **to build only** — the output is a standalone binary with no runtime deps.

```sh
make build-linux     # -> dist/csat-linux-amd64  (static linux/amd64)
make build           # -> dist/csat              (your OS, for local testing)
```

## 2. Install on the host
```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin csat
sudo install -m755 dist/csat-linux-amd64 /usr/local/bin/csat
sudo mkdir -p /etc/csat /var/lib/csat && sudo chown csat:csat /var/lib/csat

sudo cp config.example.toml /etc/csat/config.toml     # edit site.name, display_timezone, db.path
sudo cp .env.example        /etc/csat/csat.env        # set CSAT_ADMIN_INITIAL_PW
sudo chmod 600 /etc/csat/config.toml /etc/csat/csat.env

sudo cp deploy/csat.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now csat
curl -fsS http://127.0.0.1:8080/healthz                # -> ok
```

First boot creates the DB, seeds the `admin` user (password = `CSAT_ADMIN_INITIAL_PW`, force-changed
on first login), and — if `CSAT_CRYPTO_SECRET` is unset — generates the per-deployment token secret
(shown in the log and at `/settings`).

## 3. Branding (optional)
- **Logo:** drop a file named `logo.svg` / `logo.png` / `logo.webp` / `logo.jpg` next to
  `config.toml`. Auto-detected, no restart needed.
- **Theme:** set `[branding] theme_color = "#0f766e"`.

## 4. TLS
- **Behind your proxy (recommended):** keep `server.tls.mode = "off"`, point the proxy at the
  listen address, and set `trust_proxy = true` + `trusted_proxies`.
- **Built-in:** `server.tls.mode = "autocert"`, list `domains`, open ports 80/443 (uncomment
  `AmbientCapabilities=CAP_NET_BIND_SERVICE` in the unit).

## 5. Wire up the SMS link
At end of call, your platform builds the survey link with the shared `crypto_secret` (copy it from
`/settings`) and texts it to the caller. Use the ready-made minters in
[`integrations/`](integrations/) — `mint_link.py` (Python) or `mint_link.js` (Node).

```sh
# quick test without your platform:
dist/csat -config config.toml -mint -cid "+15551234567" -ts $(date +%s) -lang es -base "https://csat.example.com"
```

See [`README.md`](README.md) for the full token recipe and [`deploy/README.md`](deploy/README.md)
for more deployment detail.
