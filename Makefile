BINARY  := dgx-monitor
GOFLAGS := -trimpath -ldflags="-s -w"

.PHONY: all deps build clean

all: build

deps:
	go mod tidy

build: deps
	go build $(GOFLAGS) -o $(BINARY) .

clean:
	rm -f $(BINARY)
