# fastclaw-aiphabee staging deployment

Private Cloudflare Container control service for AiphaBee dedicated research
Agent lifecycle. The Worker has no `workers.dev` or preview route and forwards
only the five lifecycle API shapes declared in `src/routes.mjs`.

The singleton Container uses PlanetScale Postgres schema
`fastclaw_aiphabee` through a dedicated login role and R2 bucket
`aiphabee-fastclaw-staging`. Local Container disk is never persistence
authority. Sandbox execution remains disabled in this control-plane service.

Required Worker secrets:

- `FASTCLAW_STORAGE_DSN`
- `FASTCLAW_OBJECT_STORE_ACCESSKEY`
- `FASTCLAW_OBJECT_STORE_SECRETKEY`
- `FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD`
- `FASTCLAW_CONTROL_API_KEY` (`fc_` plus 64 lowercase hex characters)

Deploy the target before adding the AiphaBee staging service binding:

```sh
npm ci
npm test
npx wrangler deploy
```

The injected control token is hashed into the named `aiphabee-control` API key
without logging plaintext. Bootstrap ensures the fixed non-secret template ID
`agt_1180f3adbf5bbf6608`; AiphaBee staging pins that exact identifier.
Subsequent boots ensure and reuse both records from Postgres.

Every process start runs an object-store `put/get/delete` smoke before the
gateway. Invalid R2 credentials, bucket access, or egress therefore fail closed
before the Container reports ready.
