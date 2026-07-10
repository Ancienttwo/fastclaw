import assert from "node:assert/strict";
import test from "node:test";

import { buildContainerEnvVars } from "./container-config.mjs";

const VALID_ENV = {
  FASTCLAW_BIND: "all",
  FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD: "test-password",
  FASTCLAW_CONTROL_API_KEY: "test-control-key",
  FASTCLAW_OBJECT_STORE_ACCESSKEY: "test-access-key",
  FASTCLAW_OBJECT_STORE_ACCOUNTID: "account-id",
  FASTCLAW_OBJECT_STORE_BUCKET: "bucket",
  FASTCLAW_OBJECT_STORE_PREFIX: "prefix",
  FASTCLAW_OBJECT_STORE_SECRETKEY: "test-secret-key",
  FASTCLAW_OBJECT_STORE_TYPE: "cloudflare-r2",
  FASTCLAW_OBJECT_STORE_USESSL: "true",
  FASTCLAW_PORT: "18953",
  FASTCLAW_SANDBOX_ENABLED: "false",
  FASTCLAW_STORAGE_AUTO_MIGRATE: "true",
  FASTCLAW_STORAGE_DSN: "postgres://test",
  FASTCLAW_STORAGE_TYPE: "postgres"
};

test("builds only the persistent dedicated Container environment", () => {
  assert.deepEqual(buildContainerEnvVars(VALID_ENV), {
    FASTCLAW_HOME: "/data/.fastclaw",
    FASTCLAW_BIND: "all",
    FASTCLAW_OBJECT_STORE_TYPE: "cloudflare-r2",
    FASTCLAW_OBJECT_STORE_USESSL: "true",
    FASTCLAW_PORT: "18953",
    FASTCLAW_SANDBOX_ENABLED: "false",
    FASTCLAW_STORAGE_AUTO_MIGRATE: "true",
    FASTCLAW_STORAGE_TYPE: "postgres",
    FASTCLAW_OBJECT_STORE_ACCOUNTID: "account-id",
    FASTCLAW_OBJECT_STORE_BUCKET: "bucket",
    FASTCLAW_OBJECT_STORE_PREFIX: "prefix",
    FASTCLAW_STORAGE_DSN: "postgres://test",
    FASTCLAW_OBJECT_STORE_ACCESSKEY: "test-access-key",
    FASTCLAW_OBJECT_STORE_SECRETKEY: "test-secret-key",
    FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD: "test-password",
    FASTCLAW_CONTROL_API_KEY: "test-control-key"
  });
});

test("rejects missing persistence configuration", () => {
  assert.throws(
    () => buildContainerEnvVars({ ...VALID_ENV, FASTCLAW_OBJECT_STORE_BUCKET: "" }),
    /missing FastClaw Container configuration: FASTCLAW_OBJECT_STORE_BUCKET/u
  );
});

test("rejects local storage fallback configuration", () => {
  assert.throws(
    () => buildContainerEnvVars({ ...VALID_ENV, FASTCLAW_STORAGE_TYPE: "" }),
    /FASTCLAW_STORAGE_TYPE must equal "postgres"/u
  );
  assert.throws(
    () => buildContainerEnvVars({ ...VALID_ENV, FASTCLAW_OBJECT_STORE_TYPE: "local" }),
    /FASTCLAW_OBJECT_STORE_TYPE must equal "cloudflare-r2"/u
  );
});
