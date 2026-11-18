# App browser smoke

Six short checks at the browser and public HTTP boundary:

- Projects work without a model and survive a reload.
- Model setup enables conversation; the key stays out of the window and state API.
- Missing/wrong tokens, foreign Host/Origin, and engine commands are refused.
- A paid-tool prompt survives reload; Deny makes no provider call.
- Approve submits once; replaying the approval is refused.
- Stop cancels a partial streamed reply at the upstream connection; the next turn works.

The suite runs the real `facet-app`, `facet`, Compa kernel, hook and policy.
Only the external OpenAI-compatible model and image-provider HTTP endpoints
are scripted. The provider returns a one-pixel PNG. There are no real API
keys, model calls, paid calls, host CLI wiring, or component installs.

## Run

Use Node.js 18+ and `npm ci`. Build both commands from this checkout and
use the same Compa kernel as CI (`COMPA_VERSION` in `.github/workflows/ci.yml`).
The kernel must already have its release checksum and provenance verified;
the suite doesn't download or replace it.

```sh
go build -o build/app-smoke/facet ./cmd/facet
go build -o build/app-smoke/facet-app ./cmd/facet-app
npx playwright install chromium --only-shell
FACET_APP_BINARY="$PWD/build/app-smoke/facet-app" \
FACET_APP_FACET="$PWD/build/app-smoke/facet" \
FACET_APP_KERNEL=/absolute/path/to/compa-kernel \
npm run test:app
```

On Linux, install the browser libraries too:
`npx playwright install chromium --with-deps --only-shell`.
In PowerShell, build with `.exe` filenames and set the three variables using
`$env:FACET_APP_BINARY = (Resolve-Path build/app-smoke/facet-app.exe).Path`
(likewise `FACET_APP_FACET` and `FACET_APP_KERNEL`), then `npm run test:app`.
Missing binaries fail rather than silently skipping the checks.

Each test gets an empty temporary profile: HOME, USERPROFILE, APPDATA,
LOCALAPPDATA, XDG directories and temporary files are isolated. Provider
credentials are synthetic and endpoints use dynamically allocated loopback
ports. Unrelated inherited environment settings/keys are not forwarded.
Cleanup stops only the test's process tree and removes its profile.
On Windows this cleanup is a forced tree termination, not a graceful-Quit test.

Failures retain a Playwright trace, screenshot, and App/kernel logs under
`test-results/`; the HTML report is in `playwright-report/`. Both are ignored.
There are no retries or fixed sleeps.

## CI and scope

The existing **CI** workflow runs the suite on Linux/Chromium only when
manually dispatched; it reuses that job's built binaries and verified kernel.
It uploads failure evidence for seven days. PR/push triggers and the existing
Go, installer and offline UAT coverage are unchanged.

This is a first smoke increment, not release certification. It does not test
installation/upgrade, real providers or model quality, rendered-video quality,
graceful App shutdown/child cleanup on every OS, or other browsers.
Passing it does not authorize publishing a draft or moving a release tag.
