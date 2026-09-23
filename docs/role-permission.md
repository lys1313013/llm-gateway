# 角色权限设计

仿照 new-api 三级角色体系，角色值越小权限越高。

## 角色定义

| 角色 | 值 | 说明 |
|------|----|------|
| **超级管理员** (Root) | 1 | 最高权限，可管理系统设置和管理员 |
| **管理员** (Admin) | 2 | 可管理渠道、路由、模型、用户、日志 |
| **普通用户** (Common) | 3 | 注册后的默认角色，只读 + 调用 API |

### 角色约束

- 用户只能管理比自己角色值低的用户（不能越级操作）
- 不能将自己的角色提升到 ≥ 当前角色的级别
- Root 用户不能被删除或禁用
- 用户不能删除自己

---

## 权限矩阵

### 认证与个人信息

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `POST /api/auth/login` | ✅ | ✅ | ✅ |
| `POST /api/auth/register` | ✅ | ✅ | ✅ |
| `GET /api/auth/me` | ✅ | ✅ | ✅ |
| `PUT /api/auth/change_password` | ✅ | ✅ | ✅ |

### API Key 管理（自己的）

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `GET /api/auth/api_keys` | ✅ | ✅ | ✅ |
| `POST /api/auth/api_keys` | ✅ | ✅ | ✅ |
| `PUT /api/auth/api_keys/:id` | ✅ | ✅ | ✅ |
| `PUT /api/auth/api_keys/:id/toggle` | ✅ | ✅ | ✅ |
| `DELETE /api/auth/api_keys/:id` | ✅ | ✅ | ✅ |

### Provider（渠道）管理

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `GET /api/provider` | ✅ | ✅ | ✅ |
| `GET /api/provider/presets` | ✅ | ✅ | ✅ |
| `GET /api/provider/:id` | ✅ | ✅ | ✅ |
| `POST /api/provider` | ❌ | ✅ | ✅ |
| `PUT /api/provider/:id` | ❌ | ✅ | ✅ |
| `DELETE /api/provider/:id` | ❌ | ✅ | ✅ |
| `POST /api/provider/test/connect` | ❌ | ✅ | ✅ |

### Quota 管理

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `GET /api/provider/quota` | ✅ | ✅ | ✅ |
| `GET /api/provider/:id/quota` | ✅ | ✅ | ✅ |
| `POST /api/provider/:id/quota/refresh` | ❌ | ✅ | ✅ |

### Model Route 管理

路由归属团队，**没有公共路由**。非 Root 的读写都被限制在自己团队内。

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `GET /api/route` | ❌ | ✅（本团队） | ✅（全部） |
| `GET /api/route/:id` | ❌ | ✅（本团队） | ✅（全部） |
| `POST /api/route` | ❌ | ✅（强制本团队） | ✅（须显式指定 `team_id`） |
| `PUT /api/route/:id` | ❌ | ✅（本团队，不能改归属） | ✅（可改 `team_id`） |
| `DELETE /api/route/:id` | ❌ | ✅（本团队） | ✅（全部） |

### Exposed Model 管理

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `GET /api/exposed_model` | ❌ | ✅（本团队） | ✅（全部，带 `team_name`） |
| `GET /api/exposed_model/:id` | ❌ | ✅（本团队） | ✅（全部） |
| `POST /api/exposed_model` | ❌ | ✅（强制本团队） | ✅（须显式指定 `team_id`） |
| `PUT /api/exposed_model/:id` | ❌ | ✅（本团队，不能改归属） | ✅（可改 `team_id`） |
| `DELETE /api/exposed_model/:id` | ❌ | ✅（本团队） | ✅（全部） |
| `PUT /api/exposed_model/:id/test_time` | ❌ | ✅（本团队） | ✅（全部） |

### 多租户（团队）维度

`exposed_model` 与 `model_route` 都必须归属某个团队（`team_id NOT NULL`），不存在「全局可见」的记录。

- **唯一性**：`(model_id, team_id)` 复合唯一 —— 不同团队可以各自登记同名模型；同团队内重复返回 409（错误信息带记录 ID / 团队 / 状态，便于定位）
- **Root 也是团队的一员**：管理接口里 Root 有全量视角（可以看/改所有团队），但**转发时按自己所属团队校验**，没有豁免
- **未分配团队的用户**：模型列表为空，管理接口只能建出 403，`/v1/*` 一律拒绝
- **删除团队**：名下还有模型或路由时返回 409 + 可读文案（外键 `ON DELETE RESTRICT`）
- **转发前授权闸门**：`/v1/chat/completions`、`/v1/messages`、`/v1/responses` 在匹配路由**之前**校验模型是否在调用者团队的 `exposed_model` 清单中且启用，否则 403（不是 404）。`GET /v1/models` 与闸门同一视角：列表里出现的就是你能调的
- **`/api/test/*` 不加闸门**：管理页的连通性测试要能测任意团队的模型；请求体里的 `team_id` 决定按哪个团队的路由匹配，缺省回落到调用者自己的团队
- **已知不一致**：`/api/*` 的 team 取自 JWT claim，`/v1/*` 每次从库里读 `users.team_id`。改了用户的团队归属后，重新登录前这两条路径的判定会不同
- **本次未做**：`/api/logs`、`/api/stats/*` 对 role > 2 只按 `user_id` 过滤，团队管理员仍能看到所有团队的流量

### 日志与会话

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `GET /api/logs` | ✅（仅自己） | ✅（全部） | ✅（全部） |
| `GET /api/logs/:id` | ✅（仅自己） | ✅（全部） | ✅（全部） |
| `DELETE /api/logs/:id` | ❌ | ✅ | ✅ |
| `GET /api/logs/today_stats` | ✅（仅自己） | ✅（全部） | ✅（全部） |
| `GET /api/logs/status_codes` | ✅（全部） | ✅（全部） | ✅（全部） |
| `GET /api/stats/daily_tokens` | ✅（仅自己） | ✅（全部） | ✅（全部） |
| `GET /api/sessions` | ✅（仅自己） | ✅（全部） | ✅（全部） |
| `GET /api/sessions/:id` | ✅（仅自己） | ✅（全部） | ✅（全部） |
| `DELETE /api/sessions/:id` | ❌ | ✅ | ✅ |

### 用户管理

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `GET /api/auth/users` | ❌ | ✅ | ✅ |
| `DELETE /api/auth/users/:id` | ❌ | ✅（仅普通用户） | ✅（管理员和普通用户） |

### 隐身测试

| 接口 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| `POST /api/test/chat` | ✅ | ✅ | ✅ |
| `POST /api/test/messages` | ✅ | ✅ | ✅ |

### 系统设置（规划中）

| 功能 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| 全局系统配置 | ❌ | ❌ | ✅ |
| 自定义 OAuth / OIDC | ❌ | ❌ | ✅ |
| 升降管理员 | ❌ | ❌ | ✅ |

---

## 前端路由可见性

| 页面 | 普通用户 | 管理员 | Root |
|------|:--:|:--:|:--:|
| Playground（测试） | ✅ | ✅ | ✅ |
| 模型总览 | ✅ | ✅ | ✅ |
| Provider / 路由 / 模型配置页 | 只读 | ✅ | ✅ |
| 日志 / 会话 | 仅自己 | ✅ | ✅ |
| 统计面板 | 仅自己 | ✅ | ✅ |
| API Key 管理 | 仅自己 | ✅ | ✅ |
| 用户管理 | ❌ | ✅ | ✅ |
| 系统设置 | ❌ | ❌ | ✅ |

---

## 实现要点

1. `users` 表新增 `role` 字段（INT，默认 3）
2. 认证中间件解析角色并注入 context
3. 每个 handler 入口做角色校验，admin 及以上才能执行写操作
4. 日志/会话查询自动按 user_id 过滤（普通用户仅见自己，管理员见全部）
5. 首次启动默认创建 root 用户（role=1，替代当前 admin 默认用户）
6. 前端侧边栏和路由根据角色动态渲染
