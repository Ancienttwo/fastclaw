const CONTROL_ROUTES = [
  { method: "GET", pattern: /^\/api\/status$/u },
  { method: "POST", pattern: /^\/v1\/users$/u },
  { method: "POST", pattern: /^\/api\/users\/[^/]+\/agents$/u },
  { method: "PUT", pattern: /^\/api\/users\/[^/]+$/u },
  { method: "DELETE", pattern: /^\/api\/users\/[^/]+$/u }
];

export function isAllowedControlRequest(method, pathname) {
  return CONTROL_ROUTES.some(
    (route) => route.method === method.toUpperCase() && route.pattern.test(pathname)
  );
}
