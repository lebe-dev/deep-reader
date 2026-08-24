# Building and deploying the iOS/Android apps

Deep Reader's SvelteKit frontend is packaged into native iOS and Android apps
with [CapacitorJS](https://capacitorjs.com/). This is for **personal sideload
use only** — installing on your own devices, no App Store / Play Store release.

The native app loads the same static bundle as the web PWA (`frontend/build`)
and talks to a **remote** Deep Reader server you configure on first launch — it
does not run a backend on the device.

## Prerequisites

- **iOS**: a Mac with Xcode installed, and an Apple ID signed into Xcode
  (free Apple Development provisioning is enough — no paid account required,
  except for passkeys; see [Passkeys on device](#passkeys-on-device)).
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
| `IOS_DEV_TEAM_ID` | auto-detected from the keychain (see below) | Apple Developer Team ID used for code signing. |
| `IOS_DEVICE_ID` | the single connected device | UDID to target when multiple devices are connected. |

Team ID auto-detection reads the **OU** field of the signing certificate's
subject — that is where the Team ID lives. It is *not* the 10-character code in
the certificate's name (`Apple Development: you@example.com (N264NVXPH7)`);
that one identifies the certificate, and passing it as `DEVELOPMENT_TEAM` makes
`xcodebuild` fail with `No Account for Team "…"` plus `No profiles for
'ru.tinyops.deepreader' were found`. When the keychain holds signing
certificates for several teams, the one whose installed provisioning profile
already covers `ru.tinyops.deepreader` wins. Override with `IOS_DEV_TEAM_ID` if
you need a specific team.

On a free Apple Development profile, the app's provisioning expires after 7
days — just re-run `just deploy-ios` to reinstall. On first launch on device,
trust the developer certificate once under **Settings → General → VPN & Device
Management**.

`deploy-ios` also verifies the code signature of the Capacitor xcframeworks that
SwiftPM unpacks into `frontend/ios/DerivedData/SourcePackages/artifacts`. If a
tool has rewritten a file inside one of them the seal breaks and Xcode fails
with `a sealed resource is missing or invalid`; the recipe then deletes
`SourcePackages` so SwiftPM unpacks a pristine copy. Deleting only `artifacts/`
is not enough — `workspace-state.json` keeps claiming they are installed.
Formatters are kept out of these trees by `frontend/.prettierignore`.

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

## Passkeys on device

The native app can sign in with a passkey, but the OS has to be told that this
app speaks for the server's domain — and that association is baked into the
build, not read from the server URL the user types on `/connect`.

Set `PASSKEY_RP_ID` in `.env` before syncing. `just cap-sync` passes it to
`frontend/capacitor.config.ts`, which hands it to the Capacitor passkey plugin;
the plugin then writes the platform wiring into `frontend/android/**` and
`frontend/ios/**` during `npx cap sync`. Both trees are generated, so the edits
are expected and must not be hand-maintained.

The server side is the matching half — the app is only trusted if the domain
publishes an association document, which the backend serves from
`PASSKEY_ANDROID_PACKAGE` / `PASSKEY_ANDROID_FINGERPRINTS` / `PASSKEY_IOS_APP_ID`
(see [DEV.md](../DEV.md#passkeys-webauthn)). It must be reachable over real
HTTPS on the RP ID's own domain.

### Android

Works with the debug keystore `deploy-android` already uses. Take its
fingerprint and put it in the **server's** `.env`:

```sh
keytool -list -v -keystore ~/.android/debug.keystore \
    -alias androiddebugkey -storepass android -keypass android | grep SHA256
```

```
PASSKEY_ANDROID_PACKAGE=ru.tinyops.deepreader
PASSKEY_ANDROID_FINGERPRINTS=48:BC:5A:…
```

Restart the server, re-run `just cap-sync`, reinstall the app. Android caches
`assetlinks.json`; if the passkey prompt does not appear, clear the Google Play
Services storage or reinstall.

### iOS — needs a paid Apple Developer account

Passkeys on iOS require the **Associated Domains** entitlement
(`webcredentials:<domain>`), which a free Apple Development personal team cannot
provision. Adding it without a paid Apple Developer Program membership makes
`just deploy-ios` fail to find a matching provisioning profile.

The iOS wiring is therefore behind its own flag, and **off by default** — every
`cap sync` clears `PASSKEY_RP_ID` for the iOS pass so the entitlement is stripped
and iOS keeps building on a free account. When you do have a paid membership:

1. Enable the Associated Domains capability for the app id in the Apple
   Developer portal.
2. Set `PASSKEY_IOS_ENABLED=true` and `PASSKEY_IOS_APP_ID=<TeamID>.ru.tinyops.deepreader`
   in `.env`.
3. `just cap-sync && just deploy-ios`.

Setting the flag back to `false` removes the entitlement again on the next sync,
so this is not a one-way door.

Until then the iOS app signs in with the password, and passkeys work on the web
PWA and on Android.

## What's out of scope

No push notifications, no background sync, no App/Play Store distribution, no
OTA/live updates — updating the app means re-running `just deploy-ios` /
`just deploy-android`.
