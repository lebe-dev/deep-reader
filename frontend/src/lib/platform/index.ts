// Platform layer — the single place in the app that knows about Capacitor.
//
// Everything else (UI, sync engine, Dexie) branches only through these helpers,
// so the web PWA and the native iOS/Android shells share one codebase
// (MOBILE-ARCH.md §4, §6.1). `@capacitor/core` is safe to import in the web
// bundle — off-device it reports platform 'web' and every plugin is a no-op — so
// a runtime check is enough and no conditional compilation is needed.
//
// Named exports only; no default export.

import { Capacitor } from '@capacitor/core';

/** True inside a native iOS/Android shell; false in a browser (web PWA). */
export const isNative = (): boolean => Capacitor.isNativePlatform();

/** The running platform: 'ios' | 'android' | 'web'. */
export const platform = (): string => Capacitor.getPlatform();
