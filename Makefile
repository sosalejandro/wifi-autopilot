.PHONY: build test lint install once logs

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o wifi-autopilot ./cmd/wifi-autopilot

test:
	go test -race ./...

lint:
	gofmt -l . | (! grep .)
	go vet ./...

install: build
	sudo ./install.sh

once:
	sudo SOPS_AGE_KEY_FILE=/etc/wifi-autopilot/age.key ./wifi-autopilot -config /etc/wifi-autopilot/config.sops.yaml -once

logs:
	journalctl -fu wifi-autopilot
