#!/bin/bash
cd "$(dirname "$0")/frontend" || exit 1

# Non-interactive shells do not load the terminal's nvm configuration.
if ! command -v pnpm >/dev/null 2>&1; then
  export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
  if [ -s "$NVM_DIR/nvm.sh" ]; then
    . "$NVM_DIR/nvm.sh"
  fi
fi

if ! command -v pnpm >/dev/null 2>&1; then
  echo "错误：找不到 pnpm。请先激活已安装 pnpm 的 Node.js 版本，或安装 pnpm 后重试。" >&2
  exit 1
fi

exec pnpm dev
