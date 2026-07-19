# Building and deploying the iOS/Android apps

Deep Reader's SvelteKit frontend is packaged into native iOS and Android apps
with [CapacitorJS](https://capacitorjs.com/). This is for **personal sideload
use only** — installing on your own devices, no App Store / Play Store release.

The native app loads the same static bundle as the web PWA (`frontend/build`)
and talks to a **remote** Deep Reader server you configure on first launch — it
does not run a backend on the device.

## Prerequisites

- **iOS**: a Mac with Xcode installed, and an Apple ID signed into Xcode
  (free Apple Development provisioning is enough — no paid account required).
  Capacitor 8 uses Swift Package Manager, so CocoaPods is not needed.
- **Android**: Android Studio (for the SDK/build tools) or a standalone
  Android SDK with `ANDROID_SDK_ROOT`/`ANDROID_HOME` set.
- Node.js + npm (same as frontend development).
- A physical device for `deploy-ios` / `deploy-android`, or a
  simulator/emulator for `ios-run` / `android-run`.

## First-time setup

```sh
cd frontend
npm install
```

The native projects (`frontend/ios/`, `frontend/android/`) are already checked
into git — you don't need to run `cap add`.

## Building the SPA into the native projects

```sh
just cap-sync
```

Runs `npm run build` and `npx cap sync`, copying the fresh SvelteKit build into
both native shells. Run this after any frontend change you want to test on
device.

To regenerate native icons/splash screens from `frontend/assets/icon.png`:

```sh
just cap-assets
```

## Opening in the native IDEs (manual build/signing)

```sh
just ios-build       # cap-sync, then open Xcode
just android-build   # cap-sync, then open Android Studio
```

## Running on a simulator/emulator

```sh
just ios-run
just android-run
```

## Deploying to a physically connected device (no IDE)

```sh
just deploy-ios
just deploy-android
```

Both targets first run `cap-sync-versioned`, which stamps `VERSION` + the short
git commit hash into `frontend/package.json` for the duration of the build (so
the installed app reports exactly which commit it is), then restores the
original file — the stamp never lands in your working tree.

### iOS signing

`deploy-ios` runs `xcodebuild` with automatic signing, then installs and
launches via `xcrun devicectl`. Configure signing/device selection in `.env`
(gitignored, loaded automatically by the Justfile):

| Variable | Default | Purpose |
|---|---|---|
| `IOS_DEV_TEAM_ID` | auto-detected from `security find-identity -v -p codesigning` | Apple Developer Team ID used for code signing. |
| `IOS_DEVICE_ID` | the single connected device | UDID to target when multiple devices are connected. |

On a free Apple Development profile, the app's provisioning expires after 7
days — just re-run `just deploy-ios` to reinstall. On first launch on device,
trust the developer certificate once under **Settings → General → VPN & Device
Management**.

### Android signing

`deploy-android` runs `./gradlew assembleDebug` (signed with the Gradle debug
keystore — no setup needed), then `adb install -r` + launches the app.

| Variable | Default | Purpose |
|---|---|---|
| `ANDROID_SDK_ROOT` | `$ANDROID_HOME`, else `~/Library/Android/sdk` | Where `adb` is found. |
| `ANDROID_DEVICE_ID` | the single connected device | Target device when several are attached via `adb`. |

Bundle ID for both platforms: `ru.tinyops.deepreader`.

## First launch on a device

The native app has no built-in server — on first launch it lands on a
"connect to server" screen (`/connect`) asking for your Deep Reader server URL.
It validates the URL against `GET /api/config` before proceeding, then hands
off to the normal login/setup flow. The URL and auth token are mirrored into
`@capacitor/preferences` so they survive a WebView storage eviction; see
[MOBILE-ARCH.md §6.5](../MOBILE-ARCH.md) for details.

## What's out of scope

No push notifications, no background sync, no App/Play Store distribution, no
OTA/live updates — updating the app means re-running `just deploy-ios` /
`just deploy-android`.
