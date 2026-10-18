# P0-18 Web test and lint tooling (WEB-0)
Status: completed

## Goal
Add Vitest + Testing Library + jsdom, an ESLint flat config (typescript-eslint + react-hooks), and a Playwright skeleton to `vad-server/web`, with `test`, `lint` and `e2e` npm scripts.

## Changes (repo: path, created/modified/deleted, why)
All in repo `vad-server`:
- `web/package.json` (modified): new devDependencies (below); scripts `test` (`vitest run`), `test:watch` (`vitest`), `lint` (`eslint .`), `e2e` (`playwright test -c e2e/playwright.config.ts`); an `overrides` block `{"typescript-eslint": {"typescript": ">=4.8.4"}}` (see "TypeScript 7" below).
- `web/package-lock.json` (modified): from `npm install -D`.
- `web/vitest.config.ts` (created): merges `vite.config.ts`; `environment: 'jsdom'`, `setupFiles: ['./src/test/setup.ts']`, `include: ['src/**/*.test.{ts,tsx}']`, `css: false`.
- `web/src/test/setup.ts` (created): imports `@testing-library/jest-dom/vitest`, runs `cleanup()` after each test.
- `web/src/test/smoke.test.tsx` (created): renders a trivial inline component and asserts on it with `toBeInTheDocument`.
- `web/eslint.config.js` (created): flat config; `@eslint/js` recommended + `typescript-eslint` recommended (no type-aware rules) + `react-hooks` (`rules-of-hooks` error, `exhaustive-deps` warn); browser globals for app code, node globals for `*.config.*` and `e2e/`; ignores `dist`, `node_modules`, Playwright output.
- `web/e2e/playwright.config.ts` (created): `testDir: '.'`, `baseURL` from `E2E_BASE_URL`, one chromium project, list reporter.
- `web/e2e/smoke.spec.ts` (created): placeholder that loads `/`; whole file `test.skip` unless `E2E_BASE_URL` is set.
- No browsers were downloaded. `vite.config.ts` and `tsconfig.json` are unchanged: test files import from `vitest` explicitly (no globals), and the jest-dom matcher types come from `setup.ts`, which is inside `src`, so `tsc -b` already sees them.

### TypeScript 7 workaround (important)
The project uses `typescript@7.0.2`, the native compiler, which has no classic JS compiler API. `typescript-eslint@8.71.2` (latest; canary is the same) peer-requires `typescript >=4.8.4 <6.1.0` and needs that API. So:
1. `typescript6` = `npm:typescript@~6.0.3` is installed as an aliased devDependency.
2. `overrides` loosens typescript-eslint's peer range so `npm install` / `npm ci` resolve without `--legacy-peer-deps`.
3. At the top of `eslint.config.js`, a `node:module` `registerHooks` resolve hook redirects `require('typescript')` to `typescript6`, but only when it comes from `@typescript-eslint/*`, `typescript-eslint` or `ts-api-utils`. The typescript-eslint plugins are then loaded with dynamic `import()` after the hook is registered.

`tsc` / `npm run build` still use TS 7. Remove the alias, the override and the hook once typescript-eslint supports TS 7. This needs Node >= 22.15 / 23.5 (`module.registerHooks`); the host has Node 24.20.

### Added devDependencies and licences (checked in each installed package.json)
| Package | Version | Licence |
|---|---|---|
| vitest | 5.0.5 | MIT |
| @testing-library/react | 16.3.3 | MIT |
| @testing-library/dom | 10.4.2 | MIT |
| @testing-library/jest-dom | 7.0.1 | MIT |
| jsdom | 30.1.3 | MIT |
| eslint | 10.13.0 | MIT |
| @eslint/js | 10.0.1 | MIT |
| typescript-eslint | 8.71.2 | MIT |
| eslint-plugin-react-hooks | 7.1.1 | MIT |
| globals | 17.13.0 | MIT |
| @playwright/test | 1.64.1 | Apache-2.0 |
| typescript6 (alias of typescript) | 6.0.3 | Apache-2.0 |

Some new **transitive** dev-only packages use licences outside the MIT/ISC/Apache/BSD list:
- `lru-cache` ×3 and `minimatch` (via jsdom / typescript-eslint): BlueOak-1.0.0, which is permissive;
- `@csstools/color-helpers`, `@csstools/css-syntax-patches-for-csstree` (via jsdom): MIT-0;
- `mdn-data`: CC0-1.0;
- `caniuse-lite`: CC-BY-4.0, data only.

None of these is bundled into `dist`. INT should confirm they are acceptable. (lightningcss, MPL-2.0, was already in the tree.)

## Interfaces exposed
- `npm test` / `npx vitest run [files]`: tests are `src/**/*.test.{ts,tsx}`, run in jsdom with jest-dom matchers. Import `describe/it/expect/vi` from `vitest`.
- `npm run lint`: `eslint .`
- `npm run e2e`: Playwright specs in `web/e2e/*.spec.ts`. Set `E2E_BASE_URL` to run them; install a browser first with `npx playwright install chromium`.

## Commands run and results
- `npm install -D ...`: 0 vulnerabilities. A plain `typescript-eslint` install failed with ERESOLVE (TS 7 peer), which led to the workaround above.
- `npm run build`: OK (`tsc -b && vite build`).
- `npx tsc -b --noEmit`: OK.
- `npx vitest run` / `npm test`: 1 file, 1 test passed.
- `npm run lint`: 0 errors, 102 warnings.
- `npm run e2e` (no `E2E_BASE_URL`): 1 skipped, exit 0.

## Lint findings on existing code (configured, not fixed)
- 100 × `@typescript-eslint/no-explicit-any`, set to warn; mostly `src/pages/admin/Admin.tsx`, `src/pages/Files.tsx`.
- 2 × `react-hooks/exhaustive-deps` (warn), e.g. `src/pages/admin/Admin.tsx:30` (missing `onChange` dep).
- 4 × `no-unused-expressions` from the ternary-as-statement pattern `next.has(id) ? next.delete(id) : next.add(id)` (`src/components/files/useSelection.ts:16,27`, `src/pages/Files.tsx:118,129`). The rule stays an error, with `allowTernary` / `allowShortCircuit` enabled.

## Contract issues
None.

## Gaps / follow-ups
- Type-aware linting (`recommendedTypeChecked`) is not enabled; it would also depend on the TS 6 shim.
- `vitest.config.ts`, `eslint.config.js` and `e2e/` are not covered by `tsc -b` (tsconfig `include` is `src` only); only vitest/Playwright transpile them.

## Notes for dependent tasks / INT
- `npm run build` rewrote the tracked `web/tsconfig.tsbuildinfo` and `web/dist/`. These are build artifacts; decide whether to commit or restore them.
- Other web tests should be placed next to their code as `*.test.tsx` under `src/`. Helpers go in `src/test/`.
- Line endings: git warns LF→CRLF for `web/package.json` (autocrlf), which is harmless.
