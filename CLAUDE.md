# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

### 前端对话预览

日志详情的「对话预览」由 `ConversationPreview.tsx` 渲染，协议归一化逻辑全部在纯函数模块 `conversationNormalize.ts`（无 React 依赖）：

- `buildConversation(request, response, protocol)` 把 OpenAI / Anthropic 两种协议的请求+响应统一还原成 `Message[]`
- 协议判定：`protocol` 字段（`openai`/`anthropic`）优先；缺失时 `detectAnthropicRequest()` 按内容特征探测——顶层 `system` 字段或 `image`/`tool_use`/`tool_result`/`thinking` 类型的 part 判 Anthropic，`image_url`/`input_audio`/`file` part 或 `tool_calls` 字段判 OpenAI。**注意**：OpenAI 多模态消息的 content 也是数组，不能仅凭数组 content 判定（曾因此把 OpenAI 图片请求误判成 Anthropic，显示为「未知类型内容块」）
- 图片块由 `extractImageSrc()` 提取可渲染地址（OpenAI `image_url.url`、Anthropic base64 source 拼 `data:` URI、URL source 原样透出），有 `src` 时前端直接 `<Image>` 渲染，否则显示占位标签
- Anthropic `tool_result` 内容为数组时，其中的图片 part 拆分为独立 image 块

测试：`cd frontend && pnpm test`（Vitest），用例在 `conversationNormalize.test.ts`。改归一化逻辑前请先跑测试。