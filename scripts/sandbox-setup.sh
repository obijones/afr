#!/usr/bin/env bash
# Set up a throwaway project where Claude Code runs afr's hooks, without
# touching ~/.claude/settings.json. Captures land in <sandbox>/.afr-data.
#
# usage: scripts/sandbox-setup.sh [sandbox-dir]   (default: ../afr-sandbox)
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
sandbox="$(mkdir -p "${1:-$repo/../afr-sandbox}" && cd "${1:-$repo/../afr-sandbox}" && pwd)"
bin="$repo/bin/afr"

make -C "$repo" build >/dev/null
mkdir -p "$sandbox/.claude"

cmd="AFR_HOME=$sandbox/.afr-data $bin hook"
hook() { printf '[{"hooks":[{"type":"command","command":"%s"}]}]' "$cmd"; }
tool_hook() { printf '[{"matcher":"*","hooks":[{"type":"command","command":"%s"}]}]' "$cmd"; }

cat > "$sandbox/.claude/settings.local.json" <<EOF
{
  "hooks": {
    "SessionStart": $(hook),
    "UserPromptSubmit": $(hook),
    "PreToolUse": $(tool_hook),
    "PostToolUse": $(tool_hook),
    "Stop": $(hook),
    "SubagentStop": $(hook),
    "PreCompact": $(hook),
    "Notification": $(hook),
    "SessionEnd": $(hook)
  }
}
EOF

echo "Sandbox ready: $sandbox"
echo "Run:  cd $sandbox && claude"
echo "Raw payloads will appear under $sandbox/.afr-data/raw/<session>/"
