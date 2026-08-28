.PHONY: build test vet install clean

build:
	./gorun.sh build -buildvcs=false -o bin/gic-bin ./cmd/gic-bin

test:
	./gorun.sh test ./... -timeout 30s

vet:
	./gorun.sh vet ./...

# Copies the built binary onto your PATH as `gic-bin` (the shell function
# installed by `gic shell-init` expects that exact name).
install: build
	mkdir -p ~/.local/bin
	cp bin/gic-bin ~/.local/bin/gic-bin
	cp scripts/gic ~/.local/bin/gic
	chmod +x ~/.local/bin/gic

clean:
	rm -rf bin
