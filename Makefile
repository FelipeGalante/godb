.PHONY: test race fmt vet lint build run clean

test:
	go test ./...

race:
	go test -race ./...

fmt:
	gofmt -w .
	@command -v goimports >/dev/null 2>&1 && goimports -w . || true

vet:
	go vet ./...

lint:
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed; skipping"

build:
	go build -o godb ./cmd/godb

run:
	go run ./cmd/godb

clean:
	rm -f godb *.godb *.godb-journal
