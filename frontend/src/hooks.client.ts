// SvelteKit client error hook.
//
// `handleErrorWithSentry` forwards errors that SvelteKit catches in its own
// error boundary (during rendering, load, navigation) to Sentry — these never
// reach the global `onerror` handler the SDK installs, so without this hook they
// would go unreported. It is a no-op until `initSentry` has configured the SDK
// (see `$lib/sentry`), which happens after the first GET /api/config response.

import { handleErrorWithSentry } from '@sentry/sveltekit';

export const handleError = handleErrorWithSentry();
