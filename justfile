# Every recipe gets the agent's env vars, like `set -a; source agents/main/.env`.
set dotenv-path := "agents/main/.env"

project := "artosis-tts-bot"
region := "us-central1"
service := "artosis-tts-agent"
langfuse := "docker compose -f langfuse/docker-compose.yml"

[private]
default:
    @just --list

# Log in for Vertex AI and create the .env files that don't exist yet
setup:
    gcloud auth application-default login
    [ -f agents/main/.env ] || cp agents/main/.env.example agents/main/.env
    [ -f langfuse/.env ] || cp langfuse/.env.example langfuse/.env

# Chat with the agent in the terminal
console:
    go run ./agents/main console

# Agent REST API and ADK dev UI on :8080
# A run holds the request open while it reads chat (up to 60s), so the default 15s write timeout cuts it off.
web:
    go run ./agents/main web -write-timeout 2m api webui

# Frontend on :8090. Pass an Agent Engine ID to use the deployment instead of `just web`.
frontend id="":
    AGENT_ENGINE_ID={{id}} GOOGLE_CLOUD_PROJECT={{project}} GOOGLE_CLOUD_LOCATION={{region}} go run ./cmd/frontend

# Unit tests
test:
    go test ./...

# Run the dono_generator eval set as a Langfuse experiment. Langfuse evaluators score it afterwards.
eval-dono:
    RUN_EVALS=1 go test -run TestDonoEval -count=1 -v ./internal/agents/dono_generator/

# One-time: create the Langfuse evaluators and rule that score eval-dono
eval-setup:
    go run ./cmd/dono-eval-setup

# Read real chat from a live channel
test-network channel="artosis":
    TWITCH_TEST_CHANNEL={{channel}} go test -run Network -v ./internal/twitchchat/

# Start local Langfuse on :3000
langfuse-up:
    [ -f langfuse/.env ] || cp langfuse/.env.example langfuse/.env
    {{langfuse}} up -d

# Stop local Langfuse and keep its data
langfuse-down:
    {{langfuse}} down

# Stop local Langfuse and delete its data
langfuse-reset:
    {{langfuse}} down -v

# One-time GCP setup: APIs, Langfuse secrets, Secret Manager access, adkgo
gcp-setup langfuse_host public_key secret_key:
    gcloud services enable aiplatform.googleapis.com secretmanager.googleapis.com cloudbuild.googleapis.com --project {{project}}
    printf '%s' "{{langfuse_host}}" | gcloud secrets create langfuse-host --data-file=- --project {{project}}
    printf '%s' "{{public_key}}" | gcloud secrets create langfuse-public-key --data-file=- --project {{project}}
    printf '%s' "{{secret_key}}" | gcloud secrets create langfuse-secret-key --data-file=- --project {{project}}
    gcloud projects add-iam-policy-binding {{project}} \
      --member="serviceAccount:service-$(gcloud projects describe {{project}} --format='value(projectNumber)')@gcp-sa-aiplatform-re.iam.gserviceaccount.com" \
      --role=roles/secretmanager.secretAccessor
    go install google.golang.org/adk/v2/cmd/adkgo@v2.4.0

# Deploy to Agent Engine. Pass the Agent Engine ID to update an existing deployment.
deploy id="":
    adkgo deploy agentengine -p {{project}} -r {{region}} -s {{service}} -e ./agents/main {{ if id != "" { "--agent_engine_id " + id } else { "" } }}

convert:
    go run ./helper_tools/newline_remover.go