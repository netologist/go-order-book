.PHONY: build test race lint run tidy smoke test-api clean

build:
	go build ./...

test:
	go test ./...

race:
	go test -race -count=100 ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

run:
	go run ./cmd/server

# smoke starts the server, runs API tests, then shuts it down.
smoke: build
	./hack/smoke.sh

# test-api runs API tests against a running server (default :8080).
test-api:
	./hack/smoke.sh --test-only

clean:
	rm -f server
	rm -rf /tmp/go-order-book-*
