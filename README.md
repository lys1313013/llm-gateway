# Mock OpenAI API Server

## 1. 项目简介

Mock OpenAI 是一个基于 Python Flask 框架开发的大模型 API 兼容代理服务，同时兼容 **OpenAI** 和 **Anthropic** 两种 API 协议。它主要用于在开发和测试阶段替代真实的大模型 API，将请求透明转发到第三方兼容服务（如阿里云 DashScope、AWS Bedrock 等），并附带管理后台用于配置管理、请求日志查看和 Token 用量统计。

**支持的 API 接口：**
- `POST /v1/chat/completions` — OpenAI 兼容的聊天补全接口
- `GET /v1/models` — OpenAI 兼容的模型列表接口
- `POST /v1/messages` — Anthropic 兼容的 Messages API 接口

## 2. 主要功能

- **双协议支持**：
  - **OpenAI 协议**：兼容 `/v1/chat/completions` 和 `/v1/models` 接口，支持流式（SSE）和非流式响应，支持工具调用（tool_calls）和函数调用（function_call）的透传
  - **Anthropic 协议**：兼容 `/v1/messages` 接口，支持流式（SSE）和非流式响应，支持 Content Block、工具调用、扩展思考（thinking）等 Anthropic 特有功能
- **代理模式（Proxy）**：将请求透明转发到第三方兼容 API，支持流式数据的透传与日志聚合记录
- **数据库驱动的路由系统**：通过 PostgreSQL 数据库管理模型路由规则，支持通配符匹配、优先级排序、按协议过滤，路由变更无需重启服务
- **管理后台（Web UI）**：基于 React + Ant Design 的管理界面，提供大模型厂商管理、模型路由管理、模型列表管理、请求日志查看、Token 用量可视化统计等功能
- **请求日志**：所有 API 请求和响应均记录到数据库，支持按模型名称、协议类型搜索，可查看完整的请求/响应 JSON 详情
- **Token 用量统计**：自动计算并记录 Token 用量（使用 tiktoken 估算），提供每日/每小时趋势图、模型占比饼图等可视化报表
- **跨域支持 (CORS)**：自带 CORS 支持，方便前端 Web 页面或跨端应用直接发起调用测试

## 3. 技术栈

| 层级 | 技术 |
|------|------|
| 后端框架 | Python 3.x + Flask |
| 数据库 | PostgreSQL 15 |
| ORM/驱动 | psycopg2-binary |
| HTTP 转发 | requests |
| Token 估算 | tiktoken |
| 前端框架 | React 19 + TypeScript |
| UI 组件库 | Ant Design 6 |
| 图表 | @ant-design/plots 2 |
| 代码编辑器 | Monaco Editor |
| 构建工具 | Vite 8 |
| 包管理器 | pnpm |
| 容器化 | Docker + docker-compose |

## 4. 环境与依赖

### 4.1 后端

项目后端依赖 Python 3.x 运行环境，主要依赖包：

- **Flask**: Web 框架
- **Flask-CORS**: 处理跨域请求
- **Requests**: 用于代理模式下的请求转发
- **psycopg2-binary**: PostgreSQL 数据库驱动
- **tiktoken**: OpenAI Token 估算库
- **openai**: OpenAI SDK
- **python-dotenv**: 环境变量管理

安装依赖：
```bash
cd backend
pip install -r requirements.txt
```

### 4.2 数据库

项目使用 PostgreSQL 15 作为持久化存储。可通过 Docker Compose 快速启动：

```bash
docker-compose up -d
```

这将启动一个 PostgreSQL 容器（容器名 `mock_openai_postgres`），默认配置：
- 端口：`5432`
- 用户名：`postgres`
- 密码：`password`
- 数据库名：`mock_openai`

数据库连接参数可通过环境变量覆盖：

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `DB_HOST` | `localhost` | 数据库主机 |
| `DB_PORT` | `5432` | 数据库端口 |
| `DB_NAME` | `mock_openai` | 数据库名 |
| `DB_USER` | `postgres` | 数据库用户 |
| `DB_PASSWORD` | `password` | 数据库密码 |
| `DB_TIMEZONE` | `Asia/Shanghai` | 时区（用于按小时统计） |

### 4.3 前端

前端基于 React + Vite 构建，使用 pnpm 管理依赖：

```bash
cd frontend
pnpm install
```

## 5. 启动方式

### 5.1 启动数据库
```bash
docker-compose up -d
```

### 5.2 启动后端
```bash
cd backend
python app.py --port 5001
```
后端默认运行在 `http://localhost:5001`。

### 5.3 启动前端（开发模式）
```bash
cd frontend
pnpm dev
```
前端开发服务器默认运行在 `http://localhost:18888`，并自动将 `/api` 和 `/v1` 请求代理到后端 `http://localhost:5001`。

> 服务启动后，您的前端或客户端 SDK 中的 Base URL 可以配置为 `http://127.0.0.1:5001/v1`（OpenAI 协议）或 `http://127.0.0.1:5001/v1`（Anthropic 协议）。

## 6. 配置说明

项目采用 **数据库驱动** 的配置方式，所有配置通过管理后台 Web UI 或 Admin API 动态管理，无需重启服务。

### 6.1 大模型厂商（Provider）

管理第三方 API 厂商的连接信息：

| 字段 | 说明 |
|------|------|
| `name` | 厂商名称（唯一） |
| `base_url` | 接口基础地址（Base URL） |
| `api_key` | API 密钥 |
| `protocol` | 协议类型：`openai` 或 `anthropic` |
| `remark` | 备注 |

### 6.2 模型路由（Model Route）

定义请求的路由规则，将模型名称映射到目标厂商：

| 字段 | 说明 |
|------|------|
| `model_pattern` | 模型匹配规则，支持通配符（如 `gpt-4*`、`claude-*`、`*`） |
| `route_type` | 路由类型（`proxy`） |
| `provider_id` | 目标厂商 ID |
| `target_model` | 目标模型名称（可选，不填则使用原始模型名） |
| `timeout` | 请求超时时间（秒），默认 60 |
| `log_requests` | 是否记录请求日志 |
| `log_responses` | 是否记录响应日志 |
| `priority` | 优先级（数值越大越优先匹配） |
| `is_active` | 是否启用 |

**路由匹配逻辑：**
- 请求到达时，系统按优先级从高到低遍历所有启用的路由规则
- 使用 `fnmatch` 通配符匹配模型名称（如 `gpt-4*` 匹配 `gpt-4-turbo`）
- OpenAI 协议的请求只匹配 `protocol=openai` 的路由，Anthropic 协议的请求只匹配 `protocol=anthropic` 的路由
- 命中第一个匹配的规则后即停止，未匹配到任何规则则返回 404 错误

### 6.3 模型列表（Exposed Model）

管理 `GET /v1/models` 接口返回的模型列表：

| 字段 | 说明 |
|------|------|
| `model_id` | 模型 ID（唯一） |
| `owned_by` | 所属组织，默认 `organization` |
| `is_active` | 是否启用 |

管理后台还支持对暴露模型进行 OpenAI 和 Anthropic 协议的连通性测试，并记录最近测试时间。

## 7. 管理后台 API

管理后台通过 RESTful API 提供配置管理功能，所有接口以 `/api` 为前缀：

| 模块 | 接口 | 方法 |
|------|------|------|
| 厂商管理 | `/api/provider` | GET / POST |
| | `/api/provider/<id>` | GET / PUT / DELETE |
| 路由管理 | `/api/route` | GET / POST |
| | `/api/route/<id>` | GET / PUT / DELETE |
| 模型列表 | `/api/exposed_model` | GET / POST |
| | `/api/exposed_model/<id>` | GET / PUT / DELETE |
| | `/api/exposed_model/<id>/test_time` | PUT |
| 请求日志 | `/api/logs` | GET（支持 `limit`、`offset`、`model`、`protocol` 参数） |
| 今日统计 | `/api/logs/today_stats` | GET |
| Token 统计 | `/api/stats/daily_tokens` | GET（支持 `start_date`、`end_date` 参数） |

## 8. 核心代码结构

```
├── backend/
│   ├── app.py                    # Flask 主程序入口，注册路由和统计 API
│   ├── db.py                     # PostgreSQL 数据库层（DDL、CRUD、统计查询）
│   ├── token_utils.py            # Token 估算工具（tiktoken）
│   ├── requirements.txt          # Python 依赖
│   ├── routes/
│   │   ├── chat.py               # OpenAI 兼容路由：/v1/chat/completions、/v1/models
│   │   ├── anthropic.py          # Anthropic 兼容路由：/v1/messages
│   │   └── admin.py              # 管理后台 CRUD API
│   ├── services/
│   │   ├── proxy.py              # OpenAI 代理转发逻辑（流式/非流式）
│   │   └── anthropic_proxy.py    # Anthropic 代理转发逻辑（SSE/非流式）
│   └── tests/
│       ├── test_tool_call.py         # 非流式工具调用测试
│       └── test_stream_tool_call.py  # 流式工具调用测试
├── frontend/
│   ├── src/
│   │   ├── App.tsx               # 主布局（侧边栏导航 + 内容区）
│   │   ├── LogViewer.tsx         # 请求日志查看器
│   │   ├── TokenStats.tsx        # Token 用量统计仪表板
│   │   ├── ConfigProvider.tsx    # 厂商管理页面
│   │   ├── ConfigRoute.tsx       # 路由管理页面
│   │   ├── ConfigExposedModel.tsx # 模型列表管理页面
│   │   └── JsonViewer.tsx        # JSON 查看器组件（Monaco Editor）
│   ├── vite.config.ts            # Vite 配置（端口 18888，代理 /api、/v1 到后端）
│   └── package.json              # 前端依赖
├── docker-compose.yml            # PostgreSQL 容器配置
└── 需求文档.md                    # 需求文档
```

## 9. 使用示例

### 9.1 OpenAI 协议（CURL）
```bash
# 非流式请求
curl http://127.0.0.1:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any-key" \
  -d '{
    "model": "gpt-4-turbo",
    "messages": [{"role": "user", "content": "你好"}]
  }'

# 流式请求
curl http://127.0.0.1:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any-key" \
  -d '{
    "model": "gpt-4-turbo",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'

# 获取模型列表
curl http://127.0.0.1:5001/v1/models
```

### 9.2 Anthropic 协议（CURL）
```bash
# 非流式请求
curl http://127.0.0.1:5001/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: any-key" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}]
  }'

# 流式请求
curl http://127.0.0.1:5001/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: any-key" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

### 9.3 OpenAI SDK（Python）
```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:5001/v1",
    api_key="any-key"  # API Key 由路由规则中的厂商配置决定
)

response = client.chat.completions.create(
    model="gpt-4-turbo",
    messages=[{"role": "user", "content": "你好"}]
)
print(response.choices[0].message.content)
```

### 9.4 Anthropic SDK（Python）
```python
import anthropic

client = anthropic.Anthropic(
    base_url="http://127.0.0.1:5001",
    api_key="any-key"
)

message = client.messages.create(
    model="claude-sonnet-4-20250514",
    max_tokens=1024,
    messages=[{"role": "user", "content": "你好"}]
)
print(message.content[0].text)
```

## 10. 历史配置迁移

项目早期版本使用 `config.json` 文件进行配置。当前版本已迁移到数据库驱动的配置方式。首次启动时，如果检测到 `config.json` 文件存在且数据库中无厂商数据，系统会自动将 `config.json` 中的代理配置迁移到数据库中。
