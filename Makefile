.PHONY: dev build check

# The MyGO dev command auto-selects a local certificate unless -sign is explicit.
dev:
	go tool mygo dev -sign -

build:
	go tool mygo build -sign - -skip-dmg

check:
	CGO_ENABLED=0 go test ./...
	go tool mygo vet .
