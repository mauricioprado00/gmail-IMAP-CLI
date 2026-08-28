#!/bin/bash
# Helper to run go commands inside a container with network + module cache.
docker run --rm \
  -v "$(pwd)":/go/src \
  -v gic-gomodcache:/go/pkg/mod \
  -w /go/src \
  -e GOFLAGS=-mod=mod \
  -e GIT_CEILING_DIRECTORIES=/go \
  golang:1.23 go "$@"
