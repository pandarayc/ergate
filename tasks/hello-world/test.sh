#!/bin/bash
# Verify: hello.go exists AND compiles successfully.
set -e
test -f hello.go
go build -o /dev/null hello.go
