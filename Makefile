BINARY  := weather
PKG     := ./...
VERSION := 0.0.0
MONOVA  := $(shell which monova 2> /dev/null)
LDFLAGS  = -ldflags="-X weather/cmd.Version=$(VERSION)"
FDROID_REPO := jsnjack/android-apps

.NOTPARALLEL: check release

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
	curl -fsSL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.universal.md \
	    -o AGENTS.universal.md
	curl -fsSL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.go.md \
	    -o AGENTS.go.md
	curl -fsSL https://raw.githubusercontent.com/jsnjack/standards/master/AGENTS.android.md \
	    -o AGENTS.android.md

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

# Local APKs retain the existing debug certificate for update compatibility.
# Gradle runs the build on JDK 25 (gradle/gradle-daemon-jvm.properties),
# downloading it when missing, and installs the pinned SDK platform and build
# tools once the SDK licence is recorded.
ANDROID_GRADLE = set -e; \
	if [ -z "$$JAVA_HOME" ] && ! command -v java >/dev/null 2>&1; then \
	  echo "Install a Java runtime to start Gradle with: sudo dnf install java-25-openjdk-headless"; exit 1; \
	fi; \
	SDK=$${ANDROID_HOME:-$$(sed -n 's/^sdk.dir=//p' android/local.properties 2>/dev/null)}; \
	if [ ! -f "$$SDK/licenses/android-sdk-license" ]; then \
	  echo 'Set up the Android SDK with: android --sdk="$$HOME/Android/Sdk" sdk install platform-tools (see android/README.md)'; exit 1; \
	fi; \
	cd android && ./gradlew --quiet -PappVersion=$(VERSION)

apk: version
	@$(ANDROID_GRADLE) :app:assembleDebug || { \
	  echo "==> APK build failed — bootstrap the Android toolchain per android/README.md"; exit 1; }
	@mkdir -p bin
	cp android/app/build/outputs/apk/debug/app-debug.apk bin/$(BINARY)-widget.apk

android-check: apk
	@$(ANDROID_GRADLE) :app:testDebugUnitTest :app:checkRadarPolicy :app:lintDebug
	@echo "==> make android-check: all green"

install: apk
	adb install -r bin/$(BINARY)-widget.apk

release-preflight:
	@command -v gh >/dev/null 2>&1 || { echo "Install GitHub CLI with: sudo dnf install gh"; exit 1; }
	@command -v monova >/dev/null 2>&1 || { echo "Install monova with: grm install jsnjack/monova"; exit 1; }
	@gh auth status --hostname github.com >/dev/null
	@test -z "$$(git status --porcelain)" || { echo "Commit your changes and push master before make release."; exit 1; }
	@test "$$(git branch --show-current)" = master || { echo "Switch to master before make release."; exit 1; }
	@test "$$(git rev-parse HEAD)" = "$$(git ls-remote origin refs/heads/master | cut -f1)" || { echo "Push this commit to origin/master before make release."; exit 1; }

release: release-preflight check android-check
	$(MAKE) release-preflight
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
	$(MAKE) fdroid-publish

fdroid-publish: release-preflight version
	gh workflow run publish.yml --repo $(FDROID_REPO) -f app=$(BINARY) -f revision="$$(git rev-parse HEAD)" -f release_version=$(VERSION)
	@echo "F-Droid publication queued: https://github.com/$(FDROID_REPO)/actions/workflows/publish.yml"

clean:
	rm -rf bin/ $(BINARY)

.PHONY: version start build apk android-check install release-preflight release fdroid-publish test vet fmt lint check standards clean
