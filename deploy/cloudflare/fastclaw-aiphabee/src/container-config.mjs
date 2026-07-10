const REQUIRED_SECRETS = [
  "FASTCLAW_STORAGE_DSN",
  "FASTCLAW_OBJECT_STORE_ACCESSKEY",
  "FASTCLAW_OBJECT_STORE_SECRETKEY",
  "FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD",
  "FASTCLAW_CONTROL_API_KEY"
];

const REQUIRED_VALUES = {
  FASTCLAW_BIND: "all",
  FASTCLAW_OBJECT_STORE_TYPE: "cloudflare-r2",
  FASTCLAW_OBJECT_STORE_USESSL: "true",
  FASTCLAW_PORT: "18953",
  FASTCLAW_SANDBOX_ENABLED: "false",
  FASTCLAW_STORAGE_AUTO_MIGRATE: "true",
  FASTCLAW_STORAGE_TYPE: "postgres"
};

const REQUIRED_NON_EMPTY_VALUES = [
  "FASTCLAW_OBJECT_STORE_ACCOUNTID",
  "FASTCLAW_OBJECT_STORE_BUCKET",
  "FASTCLAW_OBJECT_STORE_PREFIX"
];

export function buildContainerEnvVars(env) {
  const missing = [...REQUIRED_SECRETS, ...REQUIRED_NON_EMPTY_VALUES].filter(
    (name) => typeof env[name] !== "string" || env[name].trim() === ""
  );
  if (missing.length > 0) {
    throw new Error(`missing FastClaw Container configuration: ${missing.join(", ")}`);
  }

  const invalid = Object.entries(REQUIRED_VALUES).filter(
    ([name, expected]) => env[name] !== expected
  );
  if (invalid.length > 0) {
    throw new Error(
      `invalid FastClaw Container configuration: ${invalid
        .map(([name, expected]) => `${name} must equal ${JSON.stringify(expected)}`)
        .join(", ")}`
    );
  }

  return {
    FASTCLAW_HOME: "/data/.fastclaw",
    ...Object.fromEntries(
      [
        ...Object.keys(REQUIRED_VALUES),
        ...REQUIRED_NON_EMPTY_VALUES,
        ...REQUIRED_SECRETS
      ].map((name) => [name, env[name]])
    )
  };
}
