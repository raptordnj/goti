.PHONY: android-aar apk apk-debug android-vet package-linux test race lint fuzz bench build cross gui screenshots

test:
	go test -timeout 30m ./...

race:
	go test -race -short ./...

lint:
	golangci-lint run

fuzz:
	@for pkg in $$(go list ./...); do \
		dir=$$(go list -f '{{.Dir}}' $$pkg); \
		for f in $$(grep -h -o '^func Fuzz[A-Za-z0-9_]*' $$dir/*_test.go 2>/dev/null | sed 's/func //'); do \
			go test $$pkg -run '^$$' -fuzz "^$$f$$" -fuzztime=30s || exit 1; \
		done; \
	done

bench:
	go test ./internal/downloader -run '^$$' -bench '^BenchmarkSingleVsMultipart$$' -benchtime=3x -benchmem
	go test ./internal/downloader -run '^$$' -bench '^BenchmarkInspect$$' -benchmem

build:
	CGO_ENABLED=0 go build -o bin/goti-cli ./cmd/goti-cli

cross:
	@for os in linux windows darwin; do \
		for arch in amd64 arm64; do \
			ext=""; [ $$os = windows ] && ext=".exe"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o bin/goti-cli-$$os-$$arch$$ext ./cmd/goti-cli || exit 1; \
		done; \
	done

gui:
	go build -o bin/goti ./cmd/goti

screenshots:
	go test -tags snapshot ./internal/ui/snapshot -run TestScreenshots -args -update

# package-linux builds dist/goti_$(VERSION)_linux_$(ARCH).{deb,rpm,pkg.tar.zst,tar.gz}.
VERSION ?= 0.0.0-dev
ARCH ?= $(shell go env GOARCH)
package-linux:
	scripts/package-linux.sh $(VERSION) $(ARCH)

ANDROID_NDK_ROOT ?= $(lastword $(sort $(wildcard $(ANDROID_HOME)/ndk/*)))

android-vet:
	@test -x "$(ANDROID_NDK_ROOT)/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android26-clang" || { echo "android-vet: NDK not found; set ANDROID_HOME (or ANDROID_NDK_ROOT)" >&2; exit 1; }
	GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC=$(ANDROID_NDK_ROOT)/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android26-clang go vet ./...

GOGIO ?= gogio
ANDROID_SDK_ROOT ?= $(ANDROID_HOME)
export ANDROID_SDK_ROOT ANDROID_NDK_ROOT

android-aar:
	mkdir -p android/app/libs
	$(GOGIO) -target android -buildmode archive -arch arm64,arm,amd64 -appid io.github.raptordnj.goti.gio -minsdk 26 -ldflags "-X main.version=$(VERSION)" -o android/app/libs/goti.aar ./cmd/goti

apk-debug: android-aar
	cd android && ./gradlew assembleDebug

apk: android-aar
	cd android && ./gradlew assembleRelease -PversionName=$(VERSION) -PversionCode=$$(../scripts/android-version-code.sh $(VERSION))
