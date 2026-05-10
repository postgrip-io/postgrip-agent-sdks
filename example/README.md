# example/

Runnable examples that exercise the PostGrip Agent Go SDK end-to-end against a
live runtime service. Each example is a standalone `package main` you can `go
run` once the runtime address and auth tokens are exported.

## greeting

A single-process demo: it starts a worker that registers one activity and one
workflow, then enqueues an execution of that workflow from the same process
and waits for the result.

```sh
export POSTGRIP_AGENT_LIVE_SERVER_URL=https://postgrip.app
export POSTGRIP_AGENT_AUTH_TOKEN=...           # management-side bearer token
export POSTGRIP_AGENT_ENROLLMENT_KEY=...       # agent-side enrollment key
go run ./example/greeting
```

Optional overrides:

| Variable                       | Default              |
|:-------------------------------|:---------------------|
| `POSTGRIP_AGENT_TASK_QUEUE`    | `go-example`         |
| `POSTGRIP_AGENT_ID`            | `go-example-agent`   |

The worker enrolls itself with the runtime service the first time it polls,
exchanging `POSTGRIP_AGENT_ENROLLMENT_KEY` for a refreshable agent session.
