.PHONY: run test bench lint docker
run:
	go run ./cmd/apexx
test:
	go test -race -count=1 ./...
bench:
	go test ./internal/... -run x -bench . -benchmem
lint:
	test -z "$$(gofmt -l .)" && go vet ./...
docker:
	docker build -t apexx:dev .
