BINARY  := weather
PKG     := ./...
VERSION := 0.0.0
MONOVA  := $(shell which monova 2> /dev/null)
LDFLAGS  = -ldflags="-X weather/cmd.Version=$(VERSION)"
# The personal app key: the debug keystore that local builds and every
# released APK are signed with, so either installs over the other.
KEYSTORE ?= $(HOME)/.android/debug.keystore
RELEASE_APK := android/app/build/outputs/apk/release/app-release-unsigned.apk

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

# Every agent session loads AGENTS.md whole, and Codex reads only its first
# 32 KiB: keep it under the limit (AGENTS.universal.md, "AGENTS.md size").
AGENTS_MAX_BYTES ?= 30000
agents-size:
	@size=$$(wc -c < AGENTS.md); \
	if [ "$$size" -gt $(AGENTS_MAX_BYTES) ]; then \
	  echo "AGENTS.md is $$size bytes, over the $(AGENTS_MAX_BYTES)-byte limit: move detail to docs/ (AGENTS.universal.md, \"AGENTS.md size\")"; \
	  exit 1; \
	fi

check: fmt vet build test lint agents-size
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
	@$(ANDROID_GRADLE) :app:testDebugUnitTest :app:lintDebug
	@echo "==> make android-check: all green"

# release-apk builds the optimized, non-debuggable APK that releases publish
# and signs it with the app key. The debug APK from make apk stays for
# development.
release-apk: version
	@$(ANDROID_GRADLE) :app:assembleRelease || { \
	  echo "==> release APK build failed — bootstrap the Android toolchain per android/README.md"; exit 1; }
	@mkdir -p bin/release
	set -e; \
	SDK=$${ANDROID_HOME:-$$(sed -n 's/^sdk.dir=//p' android/local.properties 2>/dev/null)}; \
	SIGNER=$$SDK/build-tools/37.0.0/apksigner; \
	"$$SIGNER" sign --ks $(KEYSTORE) --ks-pass pass:android --ks-key-alias androiddebugkey \
	  --out bin/release/$(BINARY)-widget.apk $(RELEASE_APK); \
	"$$SIGNER" verify bin/release/$(BINARY)-widget.apk

install: apk
	adb install -r bin/$(BINARY)-widget.apk

# release publishes the pushed master commit: it checks the tree, runs both
# gates, builds the CLI archives and the signed release APK, and uploads them
# to the GitHub release v<monova version> with grm, which authenticates with
# its own token. The APK keeps the asset name weather-widget.apk, so the
# latest release's download link stays the same.
release-preflight:
	@command -v grm >/dev/null 2>&1 || { echo "Install grm from https://github.com/jsnjack/grm"; exit 1; }
	@command -v monova >/dev/null 2>&1 || { echo "Install monova with: grm install jsnjack/monova"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "Commit your changes and push master before make release."; exit 1; }
	@test "$$(git branch --show-current)" = master || { echo "Switch to master before make release."; exit 1; }
	@test "$$(git rev-parse HEAD)" = "$$(git ls-remote origin refs/heads/master | cut -f1)" || { echo "Push this commit to origin/master before make release."; exit 1; }

release: release-preflight check android-check
	$(MAKE) release-preflight
	$(MAKE) release-apk
	tar -czf bin/$(BINARY)_linux_amd64.tar.gz  --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_linux_amd64
	tar -czf bin/$(BINARY)_linux_arm64.tar.gz  --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_linux_arm64
	tar -czf bin/$(BINARY)_darwin_amd64.tar.gz --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_darwin_amd64
	tar -czf bin/$(BINARY)_darwin_arm64.tar.gz --transform 's|.*/$(BINARY)_.*|$(BINARY)|' bin/$(BINARY)_darwin_arm64
	grm release jsnjack/$(BINARY) \
		-f bin/$(BINARY)_linux_amd64.tar.gz \
		-f bin/$(BINARY)_linux_arm64.tar.gz \
		-f bin/$(BINARY)_darwin_amd64.tar.gz \
		-f bin/$(BINARY)_darwin_arm64.tar.gz \
		-f bin/release/$(BINARY)-widget.apk \
		-t "v`monova`"
	$(MAKE) publish-apk

# The install page https://jsnjack.github.io/android-apps/ offers every app's
# latest APK from the "apps" release of jsnjack/android-apps.
INSTALL_PAGE := jsnjack/android-apps

# publish-apk puts the signed release APK on the install page, replacing the
# previous one, labelled with the version the page shows. make release runs
# it last; run it again to retry.
publish-apk: release-preflight release-apk
	@command -v gh >/dev/null 2>&1 || { echo "Install GitHub CLI with: sudo dnf install gh"; exit 1; }
	@gh auth status --hostname github.com >/dev/null
	gh release upload apps "bin/release/$(BINARY)-widget.apk#$$(monova)" --clobber --repo $(INSTALL_PAGE)
	@echo "==> on the install page: https://jsnjack.github.io/android-apps/"

clean:
	rm -rf bin/ $(BINARY)

.PHONY: version start build apk android-check release-apk install release-preflight release test vet fmt lint check agents-size standards publish-apk clean
