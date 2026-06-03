VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := dist/csat
PKG     := csat-$(VERSION)-linux-amd64

.PHONY: build build-linux package package-customer run test vet fmt tidy clean

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/csat

# Single static Linux/amd64 binary (pure-Go sqlite => no cgo needed).
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN)-linux-amd64 ./cmd/csat

# Release bundle: the single static binary + config templates + systemd unit +
# installer + docs, as one tarball for delivery.
package: build-linux
	rm -rf dist/$(PKG)
	mkdir -p dist/$(PKG)
	cp dist/csat-linux-amd64 dist/$(PKG)/csat
	cp config.example.toml .env.example survey.example.json INSTALL.md README.md LICENSE NOTICE dist/$(PKG)/
	cp deploy/csat.service deploy/install.sh deploy/update.sh deploy/csat-update.service deploy/csat-update.timer dist/$(PKG)/
	cp deploy/nginx-csat.conf.example deploy/apache-csat.conf.example dist/$(PKG)/
	chmod +x dist/$(PKG)/csat dist/$(PKG)/install.sh dist/$(PKG)/update.sh
	tar -C dist -czf dist/$(PKG).tar.gz $(PKG)
	@echo "packaged: dist/$(PKG).tar.gz"
	@ls -lh dist/$(PKG).tar.gz

# Per-customer bundle: same static binary + that customer's config.toml, csat.env,
# and logo (from customers/<name>/), ready to unpack + ./install.sh on their host.
#   make package-customer CUSTOMER=curacao
package-customer: build-linux
	@test -n "$(CUSTOMER)" || { echo "usage: make package-customer CUSTOMER=<name>"; exit 1; }
	@test -f customers/$(CUSTOMER)/config.toml || { echo "missing customers/$(CUSTOMER)/config.toml"; exit 1; }
	$(eval OUT := csat-$(CUSTOMER)-linux-amd64)
	rm -rf dist/$(OUT)
	mkdir -p dist/$(OUT)
	cp dist/csat-linux-amd64 dist/$(OUT)/csat
	cp customers/$(CUSTOMER)/config.toml customers/$(CUSTOMER)/csat.env dist/$(OUT)/
	-cp customers/$(CUSTOMER)/logo.* dist/$(OUT)/ 2>/dev/null
	cp deploy/csat.service deploy/install.sh deploy/update.sh deploy/csat-update.service deploy/csat-update.timer dist/$(OUT)/
	cp deploy/nginx-csat.conf.example deploy/apache-csat.conf.example dist/$(OUT)/
	cp INSTALL.md README.md survey.example.json dist/$(OUT)/
	-cp customers/$(CUSTOMER)/DEPLOY.md dist/$(OUT)/ 2>/dev/null
	chmod +x dist/$(OUT)/csat dist/$(OUT)/install.sh dist/$(OUT)/update.sh
	tar -C dist -czf dist/$(OUT).tar.gz $(OUT)
	@echo "packaged: dist/$(OUT).tar.gz"
	@ls -lh dist/$(OUT).tar.gz

run:
	go run ./cmd/csat -config config.toml

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf dist
