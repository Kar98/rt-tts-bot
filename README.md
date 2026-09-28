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

## Run locally

Needs Application Default Credentials for Vertex AI:

```bash
gcloud auth application-default login
cp agents/main/.env.example agents/main/.env   # then fill it in
```

Console:

```bash
set -a; source agents/main/.env; set +a
go run ./agents/main console
# > summarise chat for xqc, 20 seconds
```

Web page (two terminals):

```bash
set -a; source agents/main/.env; set +a
go run ./agents/main web api webui      # agent on :8080 (ADK dev UI included)

go run ./cmd/frontend                   # http://localhost:8090
```

`AGENT_URL` points the frontend at a different ADK REST base URL (default `http://localhost:8080/api`).

If the model isn't served in your region, set `MODEL` to another model, or `MODEL_LOCATION` (for example `global`) to call the model from a different location.

Tests: `go test ./...`. To check reading real chat: `TWITCH_TEST_CHANNEL=xqc go test -run Network -v ./internal/twitchchat/`.

## Langfuse

Set `LANGFUSE_HOST`, `LANGFUSE_PUBLIC_KEY` and `LANGFUSE_SECRET_KEY`. At startup these are turned into:

- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=$LANGFUSE_HOST/api/public/otel/v1/traces`
- `OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic base64(pk:sk)`

Only traces are exported. Don't set the generic `OTEL_EXPORTER_OTLP_ENDPOINT`, because that also turns on OTLP log export, which Langfuse doesn't accept. If Langfuse isn't configured, the agent logs a warning and runs without it.

On Agent Engine, `adkgo deploy` can't set custom env vars, so the values are read from Secret Manager (`langfuse-host`, `langfuse-public-key`, `langfuse-secret-key`) instead.

## Deploy to Agent Engine

One-time setup:

```bash
PROJECT=artosis-tts-bot
gcloud services enable aiplatform.googleapis.com secretmanager.googleapis.com cloudbuild.googleapis.com --project $PROJECT

printf '%s' "https://langfuse.example.com" | gcloud secrets create langfuse-host --data-file=- --project $PROJECT
printf '%s' "pk-lf-..."                    | gcloud secrets create langfuse-public-key --data-file=- --project $PROJECT
printf '%s' "sk-lf-..."                    | gcloud secrets create langfuse-secret-key --data-file=- --project $PROJECT

PN=$(gcloud projects describe $PROJECT --format='value(projectNumber)')
gcloud projects add-iam-policy-binding $PROJECT \
  --member="serviceAccount:service-$PN@gcp-sa-aiplatform-re.iam.gserviceaccount.com" \
  --role=roles/secretmanager.secretAccessor

go install google.golang.org/adk/v2/cmd/adkgo@v2.4.0
```

Deploy from the repo root:

```bash
adkgo deploy agentengine -p artosis-tts-bot -r us-central1 -s artosis-tts-agent -e ./agents/main
# later updates:
adkgo deploy agentengine -p artosis-tts-bot -r us-central1 -s artosis-tts-agent -e ./agents/main --agent_engine_id <id>
```

`TWITCH_DEFAULT_CHANNEL` can't be set on Agent Engine either, so name the channel in each request. The frontend always sends it when the field is filled in.

Point the frontend at the deployment:

```bash
AGENT_ENGINE_ID=<id> GOOGLE_CLOUD_PROJECT=artosis-tts-bot GOOGLE_CLOUD_LOCATION=us-central1 go run ./cmd/frontend
```

Cost notes: Agent Engine bills for vCPU and memory while instances run, and Vertex sessions and model tokens cost extra. Keep `max_seconds` small, because live collection holds the request open. If Agent Engine turns out too expensive, `adkgo deploy cloudrun` scales to zero, and you can point the frontend's `AGENT_URL` at the Cloud Run URL.
