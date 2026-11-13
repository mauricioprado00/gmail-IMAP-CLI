.PHONY: build test vet install clean

# Stamped into the binary and printed by `gic version`: the commit, plus
# -dirty when the working tree had uncommitted changes. Computed on the
# host because the container's git cannot read this repo (see README).
VERSION ?= $(shell git describe --always --dirty --tags 2>/dev/null || echo unknown)

build:
	./gorun.sh build -buildvcs=false -ldflags "-X main.version=$(VERSION)" -o bin/gic-bin ./cmd/gic-bin

test:
	./gorun.sh test ./... -timeout 30s

vet:
	./gorun.sh vet ./...

# Copies the built binary onto your PATH as `gic-bin` (the shell function
# installed by `gic shell-init` expects that exact name). Each file is copied
# next to its destination and renamed over it, because overwriting in place
# fails with "Text file busy" while the connection daemon runs the old
# binary; the daemon keeps running the old one until it exits idle.
install: build
	mkdir -p ~/.local/bin
	cp bin/gic-bin ~/.local/bin/gic-bin.new
	chmod 755 ~/.local/bin/gic-bin.new
	mv -f ~/.local/bin/gic-bin.new ~/.local/bin/gic-bin
	cp scripts/gic ~/.local/bin/gic.new
	chmod 755 ~/.local/bin/gic.new
	mv -f ~/.local/bin/gic.new ~/.local/bin/gic
	@echo "installed: $$(~/.local/bin/gic-bin version)"

clean:
	rm -rf bin
