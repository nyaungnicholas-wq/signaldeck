import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    // Generated build output is not authored code. `next build` is routinely
    // pointed at an alternate distDir (.next-public for the public-surface
    // build) and a previous tree is kept as .next-prev-<stamp>, so the single
    // ".next/**" entry above left ~32k diagnostics from machine-written
    // bundles in the report, burying the ones that came from src/.
    ".next-*/**",
    "playwright-report/**",
    "test-results/**",
  ]),
]);

export default eslintConfig;
