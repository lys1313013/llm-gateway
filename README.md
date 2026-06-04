# LLM Gateway

> 一个兼容 **OpenAI** 与 **Anthropic** 双协议的大模型 API 网关，用于开发、测试和生产环境的统一接入与流量管理。

[![Go](https://img.shields.io/badge/Go-1.x-00add8?logo=go)](backend/go.mod)
[![React](https://img.shields.io/badge/React-19-61dafb?logo=react)](frontend/package.json)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15-336791?logo=postgresql)](docker-compose.yml)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)

---

## 为什么选择 LLM Gateway

- **一套接入，双协议兼容** —— 同一个网关同时暴露 OpenAI `/v1/chat/completions` 与 Anthropic `/v1/messages` 接口，客户端无需关心后端实际协议。
- **数据库驱动路由** —— 通配符匹配 + 优先级排序，路由规则变更即时生效，无需重启。
- **完整的可观测性** —— 全量请求/响应日志、Token 用量统计、每日/每小时趋势图、模型占比饼图。
- **开箱即用的管理后台** —— React + Ant Design 构建的 Web UI，厂商、路由、模型、日志、统计一站管理。
- **流式透明转发** —— SSE 流式响应逐块透传，后台聚合记录，不增加客户端延迟。

---

## 目录

- [快速开始](#快速开始)
- [支持的接口](#支持的接口)
- [架构与项目结构](#架构与项目结构)
- [配置说明](#配置说明)
- [管理后台 API](#管理后台-api)
- [使用示例](#使用示例)
- [环境变量](#环境变量)
- [技术栈](#技术栈)
- [许可证](#许可证)

---

## 快速开始

三步跑起来：

```bash
# 1. 启动 PostgreSQL
docker-compose -f docker-compose.db.yml up -d

# 2. 启动后端（默认端口 5001）
cd backend
go run ./cmd/gateway

# 3. 启动管理后台（开发模式，端口 18888）
cd frontend
pnpm install
pnpm dev
```

启动后：
- **网关接口**：`http://127.0.0.1:5001/v1`
- **管理后台**：`http://localhost:18888`

> 客户端 SDK 的 Base URL 配置为 `http://127.0.0.1:5001/v1` 即可接入。

---

## 支持的接口

| 协议 | 接口 | 说明 |
|------|------|------|
| OpenAI | `POST /v1/chat/completions` | 聊天补全，支持流式 / 非流式、工具调用、函数调用 |
| OpenAI | `GET /v1/models` | 模型列表 |
| Anthropic | `POST /v1/messages` | Messages API，支持流式 / 非流式、Content Block、工具调用、扩展思考 |

详细协议兼容性（请求/响应字段、SSE 事件类型、错误格式等）参见 [需求文档](需求文档.md)。

---

## 架构与项目结构

```
.
├── backend/
│   ├── cmd/gateway/               # HTTP 服务入口
│   ├── internal/
│   │   ├── auth/                  # 密码 + JWT + API Key
│   │   ├── config/                # env 加载
│   │   ├── db/                    # pgx 连接池 + schema + CRUD
│   │   ├── handlers/              # gin 路由（chat / anthropic / admin / auth / test）
│   │   ├── middleware/            # auth + CORS
│   │   ├── models/                # 领域类型
│   │   ├── proxy/                 # OpenAI / Anthropic 转发 + SSE
│   │   └── token/                 # tiktoken + 用量归一化
│   ├── Dockerfile                 # 多阶段构建（distroless）
│   ├── go.mod
│   └── README.md
├── frontend/
│   ├── src/
│   │   ├── App.tsx                # 主布局（侧边栏 + 内容区）
│   │   ├── ConfigProvider.tsx     # 厂商管理
│   │   ├── ConfigRoute.tsx        # 路由管理
│   │   ├── ConfigExposedModel.tsx # 模型列表管理
│   │   ├── ConfigManager.tsx      # 配置管理入口
│   │   ├── LogViewer.tsx          # 请求日志查看
│   │   ├── TokenStats.tsx         # Token 用量统计仪表板
│   │   └── JsonViewer.tsx         # JSON 查看器（Monaco Editor）
│   ├── vite.config.ts             # Vite 配置（端口 18888，代理 /api、/v1）
│   └── package.json
├── docker-compose.db.yml         # PostgreSQL 容器
├── README.md
└── 需求文档.md
```

---

## 配置说明

所有配置通过管理后台 Web UI 或 Admin API 动态管理，变更即时生效，无需重启。

### 大模型厂商（Provider）

管理第三方 API 厂商的连接信息：

| 字段 | 说明 |
|------|------|
| `name` | 厂商名称（唯一） |
| `base_url` | 接口基础地址 |
| `api_key` | API 密钥 |
| `protocol` | `openai` 或 `anthropic` |

### 模型路由（Model Route）

定义请求路由规则，将模型名映射到目标厂商：

| 字段 | 说明 |
|------|------|
| `model_pattern` | 通配符匹配（如 `gpt-4*`、`claude-*`、`*`） |
| `provider_id` | 目标厂商 ID |
| `target_model` | 目标模型名（可选，不填则保留原始模型名） |
| `timeout` | 请求超时（秒），`-1` 表示不超时（永久等待），其他正整数为超时秒数，默认 `-1` |
| `priority` | 优先级，数值越大越先匹配 |
| `log_requests` / `log_responses` | 日志开关 |
| `is_active` | 是否启用 |

**匹配逻辑**：按优先级降序遍历启用规则，用 `fnmatch` 通配符匹配模型名，OpenAI / Anthropic 协议路由相互隔离，命中首条即停止，无匹配则返回 404。

### 暴露模型（Exposed Model）

控制 `GET /v1/models` 返回的模型列表，并支持对每个模型进行 OpenAI / Anthropic 协议连通性测试。

---

## 管理后台 API

所有接口以 `/api` 为前缀，RESTful 风格：

| 模块 | 资源路径 | 支持方法 |
|------|----------|----------|
| 厂商 | `/api/provider[/<id>]` | GET / POST / PUT / DELETE |
| 路由 | `/api/route[/<id>]` | GET / POST / PUT / DELETE |
| 模型 | `/api/exposed_model[/<id>]` | GET / POST / PUT / DELETE |
| 模型测试 | `/api/exposed_model/<id>/test_time` | PUT |
| 请求日志 | `/api/logs` | GET（`limit` / `offset` / `model` / `protocol`） |
| 今日统计 | `/api/logs/today_stats` | GET |
| Token 统计 | `/api/stats/daily_tokens` | GET（`start_date` / `end_date`） |

---

## 使用示例

### OpenAI 协议

```bash
curl http://127.0.0.1:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any-key" \
  -d '{
    "model": "gpt-4-turbo",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:5001/v1", api_key="any-key")
resp = client.chat.completions.create(
    model="gpt-4-turbo",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)
```

### Anthropic 协议

```bash
curl http://127.0.0.1:5001/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: any-key" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

```python
import anthropic

client = anthropic.Anthropic(base_url="http://127.0.0.1:5001", api_key="any-key")
msg = client.messages.create(
    model="claude-sonnet-4-20250514",
    max_tokens=1024,
    messages=[{"role": "user", "content": "你好"}],
)
print(msg.content[0].text)
```

> 更多示例（流式请求、工具调用等）请参见 [需求文档](需求文档.md)。

---

## 环境变量

数据库连接参数可通过环境变量覆盖：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DB_HOST` | `localhost` | 数据库主机 |
| `DB_PORT` | `5432` | 数据库端口 |
| `DB_NAME` | `mock_openai` | 数据库名 |
| `DB_USER` | `postgres` | 用户名 |
| `DB_PASSWORD` | `password` | 密码 |
| `DB_TIMEZONE` | `Asia/Shanghai` | 时区（用于按小时统计） |

---

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.x + Gin |
| 数据库 | PostgreSQL 15（pgx v5 / pgxpool） |
| HTTP 转发 | net/http + fasthttp |
| Token 估算 | tiktoken-go |
| 前端 | React 19 + TypeScript + Vite 8 |
| UI | Ant Design 6 + @ant-design/plots 2 |
| 代码编辑器 | @monaco-editor/react |
| 包管理 | pnpm |
| 容器化 | Docker + docker-compose |

---

## 许可证

[MIT](LICENSE)
