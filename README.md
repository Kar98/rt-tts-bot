# artosis-tts-agent

An ADK Go agent that reads a Twitch channel's chat and summarises it in at most 300 characters.

- `twitch_chat_agent` (main agent) calls `read_twitch_chat`, then the `chat_summariser` subagent, and replies with the summary.
- `read_twitch_chat` joins the channel anonymously and listens until `max_seconds` (≤60) or `max_messages` (≤300) is reached. The transcript is stored in session state (`chat_transcript`) rather than passed through the main model. `source: "stored"` is a stub that reports "not implemented".
- The summariser's output is hard-truncated to 300 characters.
- Traces go to a self-hosted Langfuse over OTLP/HTTP when it's configured.

## Layout

| Path | What |
| --- | --- |
| `agents/main` | Agent entry point (local launcher, or Agent Engine when `GOOGLE_CLOUD_AGENT_ENGINE_ID` is set) |
| `internal/twitchchat` | `Source` interface, `LiveSource` (Twitch IRC), `StoredSource` stub |
| `internal/tools` | `read_twitch_chat` tool |
| `internal/agents` | `chat_summariser` subagent |
| `internal/observability` | Langfuse → `OTEL_*` env setup |
| `cmd/frontend` | Web page with a "Summarise chat" button, on `:8090` |
| `langfuse` | Docker Compose for a local Langfuse |
| `justfile` | Commands to run, test and deploy |

## Run locally

Commands are [just](https://github.com/casey/just) recipes, and `just` lists them. Every recipe loads `agents/main/.env`.

`just setup` logs in with Application Default Credentials for Vertex AI and copies the `.env.example` files that haven't been copied yet. Fill in `agents/main/.env` afterwards.

Console:

```bash
just console
# > summarise chat for xqc, 20 seconds
```

Web page (two terminals):

```bash
just web        # agent on :8080 (ADK dev UI included)
just frontend   # http://localhost:8090
```

`AGENT_URL` points the frontend at a different ADK REST base URL (default `http://localhost:8080/api`).

If the model isn't served in your region, set `MODEL` to another model, or `MODEL_LOCATION` (for example `global`) to call the model from a different location.

Tests: `just test`. To check reading real chat: `just test-network xqc`.

## Langfuse

Set `LANGFUSE_HOST`, `LANGFUSE_PUBLIC_KEY` and `LANGFUSE_SECRET_KEY`. At startup these are turned into:

- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=$LANGFUSE_HOST/api/public/otel/v1/traces`
- `OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic base64(pk:sk)`
- `OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=SPAN_ONLY`, so model and agent spans carry their prompts and responses. Without it Langfuse shows their input and output as empty.

Any of these already set in the environment are left alone. Only traces are exported. Don't set the generic `OTEL_EXPORTER_OTLP_ENDPOINT`, because that also turns on OTLP log export, which Langfuse doesn't accept. If Langfuse isn't configured, the agent logs a warning and runs without it.

### Local Langfuse

`langfuse/docker-compose.yml` runs Langfuse v4 (web, worker, Postgres, ClickHouse, Redis, MinIO) with a seeded project and API keys:

```bash
just langfuse-up
```

The UI is at http://localhost:3000 (login `admin@example.com` / `password123`). First start takes a minute while migrations run. In `agents/main/.env` set:

```bash
LANGFUSE_HOST=http://localhost:3000
LANGFUSE_PUBLIC_KEY=pk-lf-local
LANGFUSE_SECRET_KEY=sk-lf-local
```

`just langfuse-down` stops it, and `just langfuse-reset` also deletes the data. The seeded keys only apply on first start, so change them in `langfuse/.env` before then or run `just langfuse-reset` afterwards.

On Agent Engine, `adkgo deploy` can't set custom env vars, so the values are read from Secret Manager (`langfuse-host`, `langfuse-public-key`, `langfuse-secret-key`) instead.

## Deploy to Agent Engine

One-time setup. This enables the APIs, stores the Langfuse values in Secret Manager, lets the Agent Engine service account read them, and installs `adkgo`:

```bash
just gcp-setup https://langfuse.example.com pk-lf-... sk-lf-...
```

Deploy:

```bash
just deploy
# later updates:
just deploy <id>
```

`TWITCH_DEFAULT_CHANNEL` can't be set on Agent Engine either, so name the channel in each request. The frontend always sends it when the field is filled in.

Point the frontend at the deployment:

```bash
just frontend <id>
```

Cost notes: Agent Engine bills for vCPU and memory while instances run, and Vertex sessions and model tokens cost extra. Keep `max_seconds` small, because live collection holds the request open. If Agent Engine turns out too expensive, `adkgo deploy cloudrun` scales to zero, and you can point the frontend's `AGENT_URL` at the Cloud Run URL.
