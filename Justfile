# Load .env (IOS_DEV_TEAM_ID, IOS_DEVICE_ID, ANDROID_* overrides for mobile deploy).
set dotenv-load := true

# --- Variables ---
version := `tr -d '[:space:]' < VERSION 2>/dev/null || echo "0.0.0"`
imageName := 'tinyops/deep-reader'
# Mobile sideload builds stamp VERSION + short commit into package.json so an
# installed app reports exactly which commit it is (MOBILE-ARCH.md §10.4).
gitShortHash := `git rev-parse --short HEAD 2>/dev/null || echo "nogit"`
mobileVersion := version + "+" + gitShortHash

# --- Utility ---
default:
    @just --list

cleanup:
    rm -f ./bin/deep-reader

# --- Dependencies ---
bump-backend-deps:
    go get -u ./...
    go mod tidy

bump-frontend-deps:
    cd frontend && npm update

bump-deps: bump-backend-deps && bump-frontend-deps

# --- Build ---
build-frontend:
    cd frontend && npm install && npm pkg set version="{{ version }}" && npm run build
    rm -rf web/dist
    mkdir -p web/dist
    cp -r frontend/build/. web/dist/
    touch web/dist/.gitkeep

build: build-frontend && format
    go build -ldflags="-s -w -X deep-reader/internal/version.Version={{ version }}" -o ./bin/deep-reader ./cmd/server

# --- Lints ---
lint-backend: format
    go vet ./...
    golangci-lint run ./cmd/... ./internal/...

lint-frontend:
    cd frontend && npm run lint && npm run check

lint: format
    just lint-backend
    just lint-frontend

# --- Tests ---
test-backend name="":
    #!/usr/bin/env sh
    if [ -z "{{ name }}" ]; then
        go test ./...
    else
        go test ./... -run "{{ name }}"
    fi

test-frontend:
    cd frontend && npm run check && npm run test

test: test-backend && test-frontend

# --- Coverage ---
coverage:
    go test ./... -coverprofile=coverage.out
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report generated at coverage.html"

# --- Format ---
format:
    go fmt ./...
    cd frontend && npm run format

# --- Development ---
run-backend:
    go run ./cmd/server

run-frontend:
    cd frontend && npm run dev

stop:
    lsof -ti :4200 | xargs kill -9
    lsof -ti :18080 | xargs kill -9

start-env: stop-env
    docker compose up -d

stop-env:
    docker compose down

logs:
    docker compose logs -f

reset-env: stop-env
    @rm -rf data
    @echo "Dev environment data removed. Run 'just start-env' to restart."

# --- Image ---
build-image: test && lint
    docker build --progress=plain --platform linux/amd64 -t {{ imageName }}:{{ version }} .

push-image:
    docker push {{ imageName }}:{{ version }}

release-image: build-image && push-image

release: release-image

deploy:
    ssh kaiman "cd /opt/deep-reader && sed -i 's|{{ imageName }}:[^\"]*|{{ imageName }}:{{ version }}|' docker-compose.yml && docker compose pull && docker compose down && docker compose up -d"

ssh:
    ssh kaiman

# ============================================================
# Mobile (Capacitor) — personal sideload builds, no store (MOBILE-ARCH.md §10)
# ============================================================

# Rebuild the SPA and copy the bundle into the native iOS/Android projects.
cap-sync:
    cd frontend && npm run build && npx cap sync

# Regenerate native app icons + splash from frontend/assets/icon.png.
# The PWA step errors (we ship hand-made web icons in static/) and is ignored;
# iOS/Android assets are written before it, so this still does its job.
cap-assets:
    cd frontend && npx @capacitor/assets generate \
        --iconBackgroundColor '#ffffff' --iconBackgroundColorDark '#0a0a0a' \
        --splashBackgroundColor '#ffffff' --splashBackgroundColorDark '#0a0a0a' || true

# cap-sync, but stamp VERSION+githash into package.json for the duration of the
# build so the installed app reports what it is. The original is backed up and
# restored via trap (a file copy, not `git checkout`, so uncommitted changes to
# package.json survive) — the stamp never lands in the working tree.
cap-sync-versioned:
    #!/usr/bin/env bash
    set -euo pipefail
    cd frontend
    cp package.json .package.json.bak
    trap 'mv -f .package.json.bak package.json' EXIT
    npm pkg set version="{{ mobileVersion }}"
    npm run build
    npx cap sync

# Open the native IDEs for manual build / signing.
ios-build: cap-sync
    cd frontend && npx cap open ios

android-build: cap-sync
    cd frontend && npx cap open android

# Run on a simulator / emulator from the CLI.
ios-run:
    cd frontend && npm run build && npx cap sync ios && npx cap run ios

android-run:
    cd frontend && npm run build && npx cap sync android && npx cap run android

# Build, install and launch on a physically-connected iPhone without Xcode.
# Signing team: IOS_DEV_TEAM_ID (.env) else the free Apple Development identity.
# Device: IOS_DEVICE_ID (.env) else the single connected device.
deploy-ios: cap-sync-versioned
    #!/usr/bin/env bash
    set -euo pipefail

    PROJ="frontend/ios/App/App.xcodeproj"
    DERIVED="frontend/ios/DerivedData"
    BUNDLE_ID="ru.tinyops.deepreader"

    TEAM="${IOS_DEV_TEAM_ID:-}"
    if [ -z "$TEAM" ]; then
        TEAM=$(security find-identity -v -p codesigning \
            | sed -n 's/.*Apple Development:.*(\([A-Z0-9]\{10\}\)).*/\1/p' | head -1)
    fi
    [ -n "$TEAM" ] || { echo "✗ No signing team. Set IOS_DEV_TEAM_ID in .env or sign in to Xcode." >&2; exit 1; }

    UDID="${IOS_DEVICE_ID:-}"
    if [ -z "$UDID" ]; then
        JSON=$(mktemp)
        xcrun devicectl list devices --json-output "$JSON" >/dev/null
        UDID=$(node -e "const fs=require('fs');const d=JSON.parse(fs.readFileSync('$JSON','utf8'));const v=(d.result.devices||[]).find(x=>x.hardwareProperties&&x.hardwareProperties.udid);process.stdout.write(v?v.hardwareProperties.udid:'')")
        rm -f "$JSON"
    fi
    [ -n "$UDID" ] || { echo "✗ No connected iPhone. Unlock+trust it, or set IOS_DEVICE_ID in .env." >&2; exit 1; }

    echo "▸ Building for team $TEAM, device $UDID"
    xcodebuild \
        -project "$PROJ" \
        -scheme App \
        -configuration Debug \
        -destination "id=$UDID" \
        -derivedDataPath "$DERIVED" \
        -allowProvisioningUpdates \
        DEVELOPMENT_TEAM="$TEAM" \
        CODE_SIGN_STYLE=Automatic \
        build

    APP="$DERIVED/Build/Products/Debug-iphoneos/App.app"
    [ -d "$APP" ] || { echo "✗ Build product missing at $APP" >&2; exit 1; }

    echo "▸ Installing $APP"
    xcrun devicectl device install app --device "$UDID" "$APP"
    echo "▸ Launching $BUNDLE_ID"
    xcrun devicectl device process launch --device "$UDID" "$BUNDLE_ID"
    echo "✓ Deployed {{ mobileVersion }} to $UDID"

# Build, install and launch on a connected Android device (debug keystore).
# Device: ANDROID_DEVICE_ID (.env) when several are attached.
deploy-android: cap-sync-versioned
    #!/usr/bin/env bash
    set -euo pipefail

    export ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-$HOME/Library/Android/sdk}}"
    ADB="$ANDROID_SDK_ROOT/platform-tools/adb"
    command -v "$ADB" >/dev/null 2>&1 || ADB="$(command -v adb || true)"
    [ -n "$ADB" ] || { echo "✗ adb not found. Set ANDROID_SDK_ROOT in .env." >&2; exit 1; }

    TARGET=()
    [ -n "${ANDROID_DEVICE_ID:-}" ] && TARGET=(-s "$ANDROID_DEVICE_ID")

    cd frontend/android
    ./gradlew assembleDebug
    APK="app/build/outputs/apk/debug/app-debug.apk"
    [ -f "$APK" ] || { echo "✗ APK missing at $APK" >&2; exit 1; }

    echo "▸ Installing $APK"
    "$ADB" "${TARGET[@]}" install -r "$APK"
    echo "▸ Launching ru.tinyops.deepreader"
    "$ADB" "${TARGET[@]}" shell monkey -p ru.tinyops.deepreader -c android.intent.category.LAUNCHER 1 >/dev/null
    echo "✓ Deployed {{ mobileVersion }}"
