import assert from "node:assert/strict";
import test from "node:test";

import { isAllowedControlRequest } from "./routes.mjs";

test("allows only the dedicated lifecycle control surface", () => {
  const allowed = [
    ["GET", "/api/status"],
    ["POST", "/v1/users"],
    ["POST", "/api/users/u_123/agents"],
    ["PUT", "/api/users/u_123"],
    ["DELETE", "/api/users/u_123"]
  ];
  for (const [method, path] of allowed) {
    assert.equal(isAllowedControlRequest(method, path), true, `${method} ${path}`);
  }
});

test("rejects adjacent and public FastClaw surfaces", () => {
  const denied = [
    ["GET", "/healthz"],
    ["GET", "/v1/users"],
    ["GET", "/api/users/u_123"],
    ["POST", "/api/users/u_123/agents/extra"],
    ["POST", "/v1/chat/completions"],
    ["GET", "/api/agents"],
    ["GET", "/"]
  ];
  for (const [method, path] of denied) {
    assert.equal(isAllowedControlRequest(method, path), false, `${method} ${path}`);
  }
});
