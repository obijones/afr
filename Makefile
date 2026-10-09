.PHONY: build build-windows test test-short vet clean

build:
	go build -o bin/afr ./cmd/afr

build-windows:
	GOOS=windows GOARCH=amd64 go build -o bin/afr.exe ./cmd/afr

test:
	go test ./...

test-short:
	go test -short ./...

vet:
	go vet ./...
	GOOS=windows go vet ./...

clean:
	rm -rf bin
