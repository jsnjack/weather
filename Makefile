BINARY  := weather
PKG     := ./...
VERSION := 0.0.0
MONOVA  := $(shell which monova 2> /dev/null)
LDFLAGS  = -ldflags="-X weather/cmd.Version=$(VERSION)"

export PATH := $(PATH):$(shell go env GOPATH)/bin

version:
ifdef MONOVA
override VERSION = $(shell monova)
override LDFLAGS = -ldflags="-X weather/cmd.Version=$(VERSION)"
else
	$(info "Install monova with: grm install jsnjack/monova")
endif

start:
	find . -name "*.go" | entr -sr "go build && ./${BINARY}"

test:
	go test $(PKG)

vet:
	go vet $(PKG)

fmt:
	@command -v goimports >/dev/null 2>&1 || { \
	  echo "goimports is not installed. Install it with:"; \
	  echo "  go install golang.org/x/tools/cmd/goimports@latest"; \
	  exit 1; \
	}
	goimports -w .

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint is not installed. Install it with:"; \
	  echo "  grm install golangci/golangci-lint"; \
	  exit 1; \
	}
	golangci-lint run

check: fmt vet build test lint
	@echo "==> make check: all green"

standards:
	curl -sL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.universal.md \
	    -o AGENTS.universal.md
	curl -sL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.go.md \
	    -o AGENTS.go.md

bin/$(BINARY): bin/$(BINARY)_linux_amd64
	cp $< $@
	ln -sf bin/$(BINARY) $(BINARY)
bin/$(BINARY)_linux_amd64: version
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $@
bin/$(BINARY)_linux_arm64: version
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $@
bin/$(BINARY)_darwin_amd64: version
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $@
bin/$(BINARY)_darwin_arm64: version
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $@

build: bin/$(BINARY) bin/$(BINARY)_linux_amd64 bin/$(BINARY)_linux_arm64 bin/$(BINARY)_darwin_amd64 bin/$(BINARY)_darwin_arm64

# Android widget APK — debug-signed on purpose: the widget is personal
# sideload only (see android/README.md), and the debug signature matches dev
# installs, so a release APK updates the widget in place instead of
# demanding an uninstall. Gradle needs JDK 17–21: uses JAVA_HOME when set,
# else falls back to Android Studio's bundled JBR.
apk:
	@set -e; \
	if [ -z "$$JAVA_HOME" ]; then \
	  JBR=$$(find /var/lib/flatpak/app/com.google.AndroidStudio -maxdepth 8 -type d -name jbr 2>/dev/null | head -1); \
	  if [ -n "$$JBR" ]; then export JAVA_HOME=$$JBR; fi; \
	fi; \
	cd android && ./gradlew --quiet :app:assembleDebug || { \
	  echo "==> APK build failed — bootstrap the Android toolchain per android/README.md"; exit 1; }
	@mkdir -p bin
	cp android/app/build/outputs/apk/debug/app-debug.apk bin/$(BINARY)-widget.apk

release: build apk
	tar -czf bin/$(BINARY)_linux_amd64.tar.gz  --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_linux_amd64
	tar -czf bin/$(BINARY)_linux_arm64.tar.gz  --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_linux_arm64
	tar -czf bin/$(BINARY)_darwin_amd64.tar.gz --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_darwin_amd64
	tar -czf bin/$(BINARY)_darwin_arm64.tar.gz --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_darwin_arm64
	grm release jsnjack/$(BINARY) \
		-f bin/$(BINARY)_linux_amd64.tar.gz \
		-f bin/$(BINARY)_linux_arm64.tar.gz \
		-f bin/$(BINARY)_darwin_amd64.tar.gz \
		-f bin/$(BINARY)_darwin_arm64.tar.gz \
		-f bin/$(BINARY)-widget.apk \
		-t "v`monova`"

clean:
	rm -rf bin/ $(BINARY)

.PHONY: version start build apk release test vet fmt lint check standards clean
