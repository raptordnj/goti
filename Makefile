.PHONY: test race lint fuzz build cross

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run

fuzz:
	@for pkg in $$(go list ./...); do \
		dir=$$(go list -f '{{.Dir}}' $$pkg); \
		for f in $$(grep -h -o '^func Fuzz[A-Za-z0-9_]*' $$dir/*_test.go 2>/dev/null | sed 's/func //'); do \
			go test $$pkg -run '^$$' -fuzz "^$$f$$" -fuzztime=30s || exit 1; \
		done; \
	done

build:
	CGO_ENABLED=0 go build -o bin/swiftfetch-cli ./cmd/swiftfetch-cli

cross:
	@for os in linux windows darwin; do \
		for arch in amd64 arm64; do \
			ext=""; [ $$os = windows ] && ext=".exe"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o bin/swiftfetch-cli-$$os-$$arch$$ext ./cmd/swiftfetch-cli || exit 1; \
		done; \
	done
