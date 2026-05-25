# Mock OpenAI API Server

## 1. 项目简介
Mock OpenAI 是一个基于 Python Flask 框架开发的 OpenAI API 模拟服务。它主要用于在开发和测试阶段替代真实的 OpenAI 接口（目前支持 `/v1/chat/completions`）。服务支持**Mock（模拟）模式**和**Proxy（代理）模式**，既可以返回预设的本地模拟数据，也可以作为代理将请求转发给真实的第三方 API 接口。

## 2. 主要功能
- **双模式支持**：
  - **Mock 模式**：完全本地化的模拟响应。支持自定义预设回复（`preset_responses`），能够根据请求的特定内容（如模型名称、消息内容、用户标识等）精准匹配并返回指定的响应数据。支持普通的文本响应、Function Call（旧版函数调用）以及 Tool Calls（新版工具调用）。
  - **Proxy 模式**：透明代理模式。将接收到的请求转发给真实的、兼容 OpenAI 格式的第三方接口（例如阿里云 DashScope 等），并将第三方响应返回给客户端。支持流式数据的透明转发与日志聚合打印。
- **流式输出支持 (Streaming)**：不仅能处理真实接口的流式转发，在 Mock 模式下也能高度模拟真实大模型的流式分块输出（包括打字机延迟效果、Tool Call 的流式拼接等）。
- **动态配置**：基于 `config.json` 运行。Mock 模式下每次请求都会实时读取配置，修改预设匹配规则无需重启服务。
- **跨域支持 (CORS)**：自带 CORS 支持，方便前端 Web 页面或跨端应用直接发起调用测试。

## 3. 环境与依赖
项目依赖 Python 3.x 运行环境。
主要依赖包：
- Flask: Web 框架
- Flask-CORS: 处理跨域请求
- Requests: 用于 Proxy 模式下的请求转发

安装依赖：
```bash
pip install -r requirements.txt
```

## 4. 启动方式
运行 [app.py](backend/app.py) 启动服务，默认端口为 `5001`：
```bash
cd backend
python app.py --port 5001
```
> 服务启动后，您的前端或客户端 SDK 中的 Base URL 可以配置为 `http://127.0.0.1:5001/v1`。

## 5. 配置文件说明 (`config.json`)
项目的运行行为由 backend 目录下的 `config.json` 决定（可参考 [backend/config.example.json](backend/config.example.json) ）。主要包含以下三大配置块：

### 5.1 运行模式 (`mode`)
- `"mode": "mock"`：使用本地模拟数据。
- `"mode": "proxy"`：开启代理转发。

### 5.2 代理配置 (`proxy_config`)
当 `mode` 设为 `proxy` 且 `enabled` 为 `true` 时生效：
```json
"proxy_config": {
  "enabled": true,
  "target_url": "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions",
  "api_key": "your-api-key",
  "timeout": 60,
  "model": "deepseek-v4-pro",  // 如果配置了此项，会强制覆盖请求体中的 model
  "log_requests": true,
  "log_responses": true
}
```

### 5.3 预设响应配置 (`preset_responses`)
当 `mode` 设为 `mock` 时生效。用于拦截特定请求并返回预设结果。
```json
"preset_responses": [
  {
    "match_conditions": {
      "model": "gpt-3.5-turbo",
      "stream": true,
      "messages": [{"role": "user", "content": "Tell me a joke"}]
    },
    "stream_response_chunks": [
      // stream=true 匹配成功时，流式返回这里的 chunks
    ],
    "response": {
      // stream=false 匹配成功时，返回此处的完整 JSON 响应
    }
  }
]
```
> **匹配规则**：`match_conditions` 中的所有条件（如 `model`, `stream`, `messages`, `user` 等）都必须完全匹配请求数据，才会命中该预设。未命中任何预设时，系统将生成内置的默认模拟响应。

## 6. 核心代码结构
- [backend/app.py](backend/app.py): 核心主程序。包含路由处理 `/v1/chat/completions`、Proxy 转发逻辑 (`forward_request`) 以及 Mock 拦截逻辑 (`handle_mock_request`, `get_preset_response`)。
- [backend/config.example.json](backend/config.example.json): 配置文件的结构参考。
- [backend/requirements.txt](backend/requirements.txt): Python 依赖列表。
- [backend/tests/](backend/tests/): 包含针对基本对话、Function Call、Tool Call 及流式请求的单元测试脚本。

## 7. 使用示例 (CURL)
在 Mock 模式下，直接请求本地服务测试流式输出：
```bash
curl http://127.0.0.1:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```