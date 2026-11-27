# AI 皮肤工坊（测试版） (`wishingskin.ai-skin`)

> [!NOTE]
> 本插件当前处于 `0.1.0-beta.1` 测试版阶段，视觉生成算法与前端交互仍在持续打磨中。

通过自然语言描述生成标准 64×64 Minecraft 皮肤，支持纯文本 D2-Strict 分面像素精绘、云端图像 B2-v2 正交投影与外部专用算力节点，内置 UV 合规清洗、每日限额、提交冷却与站点积分结算。

- **插件标识**：`wishingskin.ai-skin`
- **当前版本**：`0.1.0-beta.1`（Pre-release）
- **协议版本**：`protocol: 2`
- **前端入口**：
  - 管理端 `admin.html`（查看引擎可用状态、发起管理测试与清理日志）
  - 用户工作区 `studio.html`（`slot: user.tools`）

## 生成引擎模式

- **纯文本 AI 像素精绘 (`structured_llm`)**：支持 OpenAI Chat Completions 兼容接口，默认使用 `pixel_grid`（D2-Strict 分面像素网格直绘），并兼容旧版 `recipe` 结构模板。
- **云端图像正交投影 (`image_api`)**：支持 OpenAI Images 兼容接口，默认采用 `ortho_b2`（生成正交前后双视图并执行 36 色量化投影），也支持针对专门微调 UV 模型的 `direct_uv`。
- **外部专用算力节点 (`async_worker`)**：对接自定义 HTTP 推理节点（支持同步 PNG 响应或异步任务轮询）。
- **内置免密钥本地演示引擎 (`local_demo`)**：管理员开启 `allow_local_fallback` 后，可在无外部 API 时按关键词配色演示生成流程。

## 权限声明

- `routes.user` / `routes.admin`：开放用户任务生成/保存与管理端状态测试接口
- `frontend` / `user.frontend`：加载管理后台与用户端「AI 皮肤工坊」工作台
- `database`：持久化生成历史与任务运行记录
- `host.public.read` / `host.users.read`：校验用户信息与默认配额状态
- `host.textures.write` / `host.characters.write`：支持一键保存至衣柜及穿戴至角色
- `host.points.spend`：在生成并校验通过后扣除积分

## 接口路由

| 方法 | 路径 | 访问级别 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/config` | `user` | 获取当前用户可用引擎、限额与冷却配置 |
| `POST` | `/generate` | `user` | 提交 AI 皮肤生成请求 |
| `GET` | `/jobs` | `user` | 轮询进行中的异步生成任务状态 |
| `GET` | `/history` | `user` | 读取用户最近生成记录 |
| `POST` | `/history/delete` | `user` | 删除指定历史生成记录 |
| `POST` | `/save` | `user` | 将生成皮肤保存至站点材质库（可选公开）并穿戴 |
| `GET` | `/admin/status` | `admin` | 查看各引擎连通配置与插件运行状态 |
| `POST` | `/admin/test` | `admin` | 在管理端验证当前配置引擎连通性 |
| `POST` | `/admin/clear-logs` | `admin` | 清空插件运行日志 |

## 配置项

| 键名 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `default_engine` | `text` | `""` | 优先使用的引擎（`structured_llm` / `image_api` / `async_worker` / `local_demo`） |
| `llm_api_url` | `text` | `""` | 文本模型 Chat API 地址（OpenAI 兼容接口） |
| `llm_api_key` | `secret` | — | 文本模型 API Key |
| `llm_model` | `text` | `gpt-4o-mini` | 文本引擎所使用的模型标识 |
| `llm_render_mode` | `text` | `pixel_grid` | 文本策略：`pixel_grid`（D2-Strict，推荐）或 `recipe`（旧版） |
| `image_api_url` | `text` | `""` | 图像模型 API 地址（OpenAI Images 兼容接口，可选） |
| `image_api_key` | `secret` | — | 图像模型 API Key |
| `image_model` | `text` | `gpt-image-1` | 图像引擎所使用的模型标识 |
| `image_api_mode` | `text` | `ortho_b2` | 投影模式：`ortho_b2`（B2-v2 正交投影，推荐）或 `direct_uv` |
| `worker_url` | `text` | `""` | 外部自定义推理节点 URL |
| `worker_secret` | `secret` | — | 外部推理节点 Bearer Token 鉴权密钥 |
| `allow_private_worker` | `boolean` | `false` | 是否允许连接内网或局域网推理节点 IP |
| `allow_local_fallback` | `boolean` | `false` | 是否启用内置免密钥本地演示引擎 |
| `points_cost` | `number` | `0` | 每次生成消耗积分，生成成功且 UV 校验通过后扣费 |
| `daily_limit` | `number` | `5` | 每名用户每日成功生成上限（0 表示仅管理员可用） |
| `cooldown_seconds` | `number` | `15` | 同一用户连续两次提交的最小冷却秒数 |
| `allow_public_save` | `boolean` | `false` | 是否允许用户将 AI 生成的皮肤直接设为公开 |
| `timeout_seconds` | `number` | `90` | 单次 AI 任务超时秒数，有效范围 15 至 240 秒 |

## 独立开发与构建

```powershell
go test -count=1 ./plugins/ai-skin/...
go run ./tools/pack pack -dir plugins/ai-skin -out dist
```
