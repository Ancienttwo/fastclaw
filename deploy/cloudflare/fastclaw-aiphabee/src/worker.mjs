import { Container } from "@cloudflare/containers";

import { buildContainerEnvVars } from "./container-config.mjs";
import { isAllowedControlRequest } from "./routes.mjs";

export class FastClawAiphaBeeContainer extends Container {
  defaultPort = 18953;
  sleepAfter = "1m";
  enableInternet = true;
  pingEndpoint = "/healthz";
  entrypoint = ["/usr/local/bin/fastclaw-aiphabee-entrypoint"];

  constructor(ctx, env) {
    super(ctx, env);
    this.envVars = buildContainerEnvVars(env);
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
