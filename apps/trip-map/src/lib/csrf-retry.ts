// The CSRF replay now lives in the shared account package (metro uses the same boundary); this keeps the
// product's import path and its tests (scripts/csrf-retry.test.mjs) pointing at the one implementation.
export * from "@kongken/account/csrf-retry";
