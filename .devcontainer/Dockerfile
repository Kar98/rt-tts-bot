FROM mcr.microsoft.com/devcontainers/go:1-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends locales jq \
 && sed -i 's/^# *en_AU.UTF-8/en_AU.UTF-8/' /etc/locale.gen && locale-gen \
 && rm -rf /var/lib/apt/lists/*

USER vscode
# Pre-create so the named volume inherits vscode ownership
RUN mkdir -p /home/vscode/.claude /home/vscode/.config/gcloud \
 && curl -fsSL https://claude.ai/install.sh | bash