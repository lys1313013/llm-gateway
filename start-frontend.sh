#!/bin/bash
cd "$(dirname "$0")/frontend" || exit 1

# Load nvm in non-interactive shells and select the project's Node.js version.
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
if [ -s "$NVM_DIR/nvm.sh" ]; then
  . "$NVM_DIR/nvm.sh" --no-use
  if ! nvm use --silent; then
    echo "错误：请先运行 cd frontend && nvm install，安装项目要求的 Node.js 版本。" >&2
    exit 1
  fi
fi

if command -v corepack >/dev/null 2>&1; then
  exec corepack pnpm dev
fi

if ! command -v pnpm >/dev/null 2>&1; then
  echo "错误：找不到 pnpm。请安装 Node.js 24.22.1 并运行 corepack enable 后重试。" >&2
  exit 1
fi

exec pnpm dev
