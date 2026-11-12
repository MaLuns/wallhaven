.PHONY: dev build check

MYGO_BUILD_FLAGS ?= -skip-dmg

# The MyGO dev command auto-selects a local certificate unless -sign is explicit.
dev:
	go tool mygo dev -sign -

build:
	go tool mygo build $(MYGO_BUILD_FLAGS) -sign -

check:
	CGO_ENABLED=0 go test ./...
	go tool mygo vet .
