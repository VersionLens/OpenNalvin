#!/bin/sh
set -eu

NALVIN_HOME="/home/nalvin"
SSH_DIR="${NALVIN_HOME}/.ssh"
CLAUDE_DIR="${NALVIN_HOME}/.claude"
CODEX_DIR="${NALVIN_HOME}/.codex"
CODE_SERVER_DIR="${NALVIN_HOME}/.config/code-server"
CONFIG_DIR="${NALVIN_HOME}/.config"
LOCAL_DIR="${NALVIN_HOME}/.local"
LOCAL_SHARE_DIR="${LOCAL_DIR}/share"
CACHE_DIR="${NALVIN_HOME}/.cache"
NPM_DIR="${NALVIN_HOME}/.npm"
CODE_SERVER_DATA_DIR="${LOCAL_SHARE_DIR}/code-server"
CODE_SERVER_USER_DIR="${LOCAL_SHARE_DIR}/code-server/User"
CODE_SERVER_SETTINGS_FILE="${CODE_SERVER_USER_DIR}/settings.json"
WORKSPACE_DIR="${NALVIN_CODE_SERVER_WORKDIR:-/workspace}"

mkdir -p "${SSH_DIR}" "${CLAUDE_DIR}" "${CODEX_DIR}" "${CODE_SERVER_DIR}" "${LOCAL_SHARE_DIR}" "${CACHE_DIR}" "${NPM_DIR}" "${CODE_SERVER_DATA_DIR}" "${CODE_SERVER_USER_DIR}" /var/run/sshd /workspace
chown nalvin:nalvin "${NALVIN_HOME}" "${SSH_DIR}" "${CLAUDE_DIR}" "${CODEX_DIR}" "${CONFIG_DIR}" "${CODE_SERVER_DIR}" "${LOCAL_DIR}" "${LOCAL_SHARE_DIR}" "${CACHE_DIR}" "${NPM_DIR}" "${CODE_SERVER_DATA_DIR}" "${CODE_SERVER_USER_DIR}" /workspace || true
chown -R nalvin:nalvin "${CODE_SERVER_DATA_DIR}" || true
[ -e "${SSH_DIR}/authorized_keys" ] || touch "${SSH_DIR}/authorized_keys"
chmod 700 "${SSH_DIR}" || true
if [ -w "${SSH_DIR}/authorized_keys" ]; then
  chown nalvin:nalvin "${SSH_DIR}/authorized_keys" || true
  chmod 600 "${SSH_DIR}/authorized_keys" || true
fi

if [ ! -f "${CODEX_DIR}/config.toml" ]; then
  cat >"${CODEX_DIR}/config.toml" <<'EOF'
cli_auth_credentials_store = "file"
EOF
  chown nalvin:nalvin "${CODEX_DIR}/config.toml"
fi

CODE_SERVER_SETTINGS_FILE="${CODE_SERVER_SETTINGS_FILE}" python3 - <<'PY'
import json
import os
from pathlib import Path

settings_path = Path(os.environ["CODE_SERVER_SETTINGS_FILE"])
settings_path.parent.mkdir(parents=True, exist_ok=True)

data = {}
if settings_path.exists():
    try:
        data = json.loads(settings_path.read_text())
    except json.JSONDecodeError:
        data = {}

profiles = data.get("terminal.integrated.profiles.linux")
if not isinstance(profiles, dict):
    profiles = {}

zsh_profile = profiles.get("zsh")
if not isinstance(zsh_profile, dict):
    zsh_profile = {}
zsh_profile["path"] = "/bin/zsh"
profiles["zsh"] = zsh_profile
data["terminal.integrated.profiles.linux"] = profiles

data.setdefault("terminal.integrated.defaultProfile.linux", "zsh")

settings_path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n")
PY
chown nalvin:nalvin "${CODE_SERVER_SETTINGS_FILE}" || true

ssh-keygen -A

if [ ! -d "${WORKSPACE_DIR}" ]; then
  WORKSPACE_DIR="${NALVIN_HOME}"
fi

su - nalvin -c "export SHELL=/bin/zsh; export COLORTERM=truecolor; exec /usr/local/bin/code-server --auth none --bind-addr 0.0.0.0:8080 \"${WORKSPACE_DIR}\"" &
code_server_pid=$!

/usr/sbin/sshd -D -e &
sshd_pid=$!

shutdown() {
  kill "${code_server_pid}" "${sshd_pid}" 2>/dev/null || true
  wait "${code_server_pid}" "${sshd_pid}" 2>/dev/null || true
}

trap shutdown INT TERM

status=0
while :; do
  if ! kill -0 "${code_server_pid}" 2>/dev/null; then
    wait "${code_server_pid}" || status=$?
    break
  fi
  if ! kill -0 "${sshd_pid}" 2>/dev/null; then
    wait "${sshd_pid}" || status=$?
    break
  fi
  sleep 2
done

shutdown
exit "${status}"
