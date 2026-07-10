import { Container } from "@cloudflare/containers";

import { isAllowedControlRequest } from "./routes.mjs";

const REQUIRED_CONTAINER_SECRETS = [
  "FASTCLAW_STORAGE_DSN",
  "FASTCLAW_OBJECT_STORE_ACCESSKEY",
  "FASTCLAW_OBJECT_STORE_SECRETKEY",
  "FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD",
  "FASTCLAW_CONTROL_API_KEY"
];

export class FastClawAiphaBeeContainer extends Container {
  defaultPort = 18953;
  sleepAfter = "1m";
  enableInternet = true;
  pingEndpoint = "/healthz";
  entrypoint = ["/usr/local/bin/fastclaw-aiphabee-entrypoint"];

  constructor(ctx, env) {
    super(ctx, env);
    const missing = REQUIRED_CONTAINER_SECRETS.filter(
      (name) => typeof env[name] !== "string" || env[name].trim() === ""
    );
    if (missing.length > 0) {
      throw new Error(`missing FastClaw Container secrets: ${missing.join(", ")}`);
    }
    this.envVars = {
      FASTCLAW_HOME: "/data/.fastclaw",
      FASTCLAW_STORAGE_TYPE: env.FASTCLAW_STORAGE_TYPE,
      FASTCLAW_STORAGE_DSN: env.FASTCLAW_STORAGE_DSN,
      FASTCLAW_STORAGE_AUTO_MIGRATE: env.FASTCLAW_STORAGE_AUTO_MIGRATE,
      FASTCLAW_BIND: env.FASTCLAW_BIND,
      FASTCLAW_PORT: env.FASTCLAW_PORT,
      FASTCLAW_SANDBOX_ENABLED: env.FASTCLAW_SANDBOX_ENABLED,
      FASTCLAW_OBJECT_STORE_TYPE: env.FASTCLAW_OBJECT_STORE_TYPE,
      FASTCLAW_OBJECT_STORE_BUCKET: env.FASTCLAW_OBJECT_STORE_BUCKET,
      FASTCLAW_OBJECT_STORE_PREFIX: env.FASTCLAW_OBJECT_STORE_PREFIX,
      FASTCLAW_OBJECT_STORE_ACCOUNTID: env.FASTCLAW_OBJECT_STORE_ACCOUNTID,
      FASTCLAW_OBJECT_STORE_USESSL: env.FASTCLAW_OBJECT_STORE_USESSL,
      FASTCLAW_OBJECT_STORE_ACCESSKEY: env.FASTCLAW_OBJECT_STORE_ACCESSKEY,
      FASTCLAW_OBJECT_STORE_SECRETKEY: env.FASTCLAW_OBJECT_STORE_SECRETKEY,
      FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD: env.FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD,
      FASTCLAW_CONTROL_API_KEY: env.FASTCLAW_CONTROL_API_KEY
    };
  }
}

export default {
  fetch(request, env) {
    const url = new URL(request.url);
    if (!isAllowedControlRequest(request.method, url.pathname)) {
      return new Response("Not found", { status: 404 });
    }
    const instanceName = env.FASTCLAW_INSTANCE_NAME;
    if (typeof instanceName !== "string" || instanceName.trim() === "") {
      return new Response("FastClaw instance is not configured", { status: 503 });
    }
    const container = env.FASTCLAW_AIPHABEE_CONTAINER.getByName(instanceName);
    return container.fetch(request);
  }
};
