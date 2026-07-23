# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## 项目概述

LLM Gateway — 兼容 OpenAI 和 Anthropic 双协议的大模型 API 网关。Go 后端 + React 管理后台，数据库驱动路由，流式透明转发。

## 快速启动

```bash
docker-compose -f docker/docker-compose.db.yml up -d   # PostgreSQL
cd backend && go run ./cmd/gateway                      # 后端 :5002
cd frontend && pnpm install && pnpm dev                 # 前端 :18888
```

默认管理员: `root` / `llm_gateway`。前端 Vite 将 `/api`、`/v1` 代理到 `localhost:5002`。

## 架构要点

### 双通道认证

- `/api/*` → JWT Bearer token
- `/v1/*` → API Key（`x-api-key: sk-...` 或 `Authorization: Bearer sk-...`）
- 两种认证统一注入 `CtxUserID`、`CtxUsername`、`CtxUserRole`、`CtxTeamID` 到 gin context
- 角色: 1=root, 2=admin, 3=common user。`middleware.RequireAdmin()` / `RequireRoot()` 做权限门控

### 路由匹配

`model_route` 表按 `priority` 降序遍历，glob 通配符匹配 model 字段。OpenAI 和 Anthropic 路由隔离——命中路由必须有对应的 `openai_base_url` 或 `anthropic_base_url`。命中首条即停止，无匹配返回 404。

### 代理转发

`proxy.HandleOpenAI()` / `proxy.HandleAnthropic()` 修改请求体 model 字段后转发上游。流式 SSE 逐块透传，`io.ReadCloser` wrapper 在 EOF 时聚合 chunk 写入 `api_logs`。流式日志写入使用 `context.Background()`（请求 ctx 可能已被取消）。超时由 `model_route.timeout` 控制，`-1` = 永不超时。

### 数据库

`db.Init()` 通过 `CREATE TABLE IF NOT EXISTS` + `ALTER TABLE ADD COLUMN IF NOT EXISTS` 自动迁移，`db.Pool` 是全局 `*pgxpool.Pool`。