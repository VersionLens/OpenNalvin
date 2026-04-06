FROM debian:bookworm-slim

ENV HOME=/home/nalvin \
    USER=nalvin \
    SHELL=/bin/zsh \
    CODEX_HOME=/home/nalvin/.codex \
    PATH=/home/nalvin/.local/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin \
    UV_PYTHON_INSTALL_DIR=/opt/uv/python \
    UV_TOOL_BIN_DIR=/home/nalvin/.local/bin \
    BUN_INSTALL=/opt/bun \
    DEBIAN_FRONTEND=noninteractive

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      bash \
      ca-certificates \
      cargo \
      curl \
      git \
      golang-go \
      libkrb5-dev \
      libssl-dev \
      make \
      g++ \
      gcc \
      openssh-server \
      openssl \
      pkg-config \
      python3-dev \
      python3 \
      rustc \
      tini \
      unzip \
      vim \
      zsh; \
    rm -rf /var/lib/apt/lists/*; \
    useradd nalvin; \
    usermod --lock nalvin; \
    mkdir -p /workspace /var/run/sshd /home/nalvin/.ssh /home/nalvin/.claude /home/nalvin/.codex; \
    chown -R nalvin:nalvin /home/nalvin /workspace

RUN set -eux; \
    curl -fsSL https://deb.nodesource.com/setup_24.x | bash -; \
    apt-get install -y --no-install-recommends nodejs; \
    rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    export UV_INSTALL_DIR=/usr/local/bin; \
    curl -LsSf https://astral.sh/uv/install.sh | sh; \
    uv python install 3.14.3; \
    PYTHON_BIN="$(uv python find 3.14.3)"; \
    ln -sf "$PYTHON_BIN" /usr/local/bin/python; \
    ln -sf "$PYTHON_BIN" /usr/local/bin/python3; \
    if [ -x "$(dirname "$PYTHON_BIN")/pip3" ]; then ln -sf "$(dirname "$PYTHON_BIN")/pip3" /usr/local/bin/pip3; fi

RUN set -eux; \
    curl -fsSL https://bun.sh/install | bash; \
    ln -sf /opt/bun/bin/bun /usr/local/bin/bun; \
    ln -sf /opt/bun/bin/bunx /usr/local/bin/bunx

RUN curl -fsSL https://code-server.dev/install.sh | sh; \
    ln -sf /usr/bin/code-server /usr/local/bin/code-server

RUN npm install -g @anthropic-ai/claude-code @openai/codex

COPY sshd_config /etc/ssh/sshd_config
COPY entrypoint.sh /usr/local/bin/nalvin-dev-entrypoint
COPY zshenv /home/nalvin/.zshenv
COPY zshrc /home/nalvin/.zshrc

RUN chmod 0755 /usr/local/bin/nalvin-dev-entrypoint && \
    chown nalvin:nalvin /home/nalvin/.zshenv /home/nalvin/.zshrc

EXPOSE 22 8080
WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/nalvin-dev-entrypoint"]
