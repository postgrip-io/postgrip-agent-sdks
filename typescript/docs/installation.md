# Installation

```sh
npm install @postgrip/agent
```

Or with another package manager:

```sh
pnpm add @postgrip/agent
yarn add @postgrip/agent
bun add @postgrip/agent
```

To pin a specific version:

```sh
npm install @postgrip/agent@0.11.0
```

## Requirements

- **Node.js 20 or newer** (or Bun ≥ 1.0). The SDK is published as ESM only — `"type": "module"` in `package.json`.
- A reachable PostGrip Agent runtime service. The default address is `https://agentorchestrator.postgrip.app`.

## Importing

The package exposes a single entry point. All public names come from `@postgrip/agent` directly:

```ts
import {
  Client,
  Connection,
  Agent,
  // workflow runtime
  proxyActivities,
  sleep,
  condition,
  executeChild,
  continueAsNew,
  defineSignal,
  defineQuery,
  defineUpdate,
  setHandler,
  milestone,
  workflowInfo,
  // activity helpers
  activityInfo,
  heartbeat,
  activityMilestone,
  activityStdout,
  activityStderr,
  // errors
  ApplicationFailure,
  CancelledFailure,
  TimeoutFailure,
  TaskFailedError,
} from '@postgrip/agent';
```

`Agent` is also re-exported as `Worker` if you prefer the Temporal-classic name:

```ts
import { Worker } from '@postgrip/agent';
```

## ESM only

The package is published as ESM (`"type": "module"`). For CommonJS projects, use a dynamic `import('@postgrip/agent')` or migrate to ESM. The `.cjs`-only path is not supported.

## TypeScript

The package ships with TypeScript declarations (`dist/index.d.ts`). No `@types/...` package needed.

```ts
import type { WorkflowFunction, ActivityRegistry, RetryPolicy } from '@postgrip/agent';
```

## Local development from a clone

```sh
git clone https://github.com/postgrip-io/agent-sdk-typescript
cd agent-sdk-typescript
bun install --frozen-lockfile
bun run typecheck
bun run build
bun run test
```

CI uses Bun. Node + npm work too if you prefer (`npm install`, `npm run build`, `npm test`).

## Running against a local agent

For local development, point the SDK at a runtime service running on your machine:

```ts
import { Connection } from '@postgrip/agent';

const connection = await Connection.connect({
  baseUrl: 'http://127.0.0.1:4100',
  // Agent token from Settings > Organization > Agent tokens.
  headers: { Authorization: `Bearer ${process.env.POSTGRIP_AGENT_TOKEN}` },
});
```

`Connection.connect` defaults to PostGrip Cloud if `baseUrl` is omitted. For local or self-hosted development, pass `baseUrl` explicitly as shown above or set `POSTGRIP_AGENTORCHESTRATOR_URL`.
