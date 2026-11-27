# 社群与游戏服务器联动 (`wishingskin.server-sync`)

将 WishingSkin 站内的角色创建、改名、删除、材质上传以及正版绑定/解绑事件实时推送至 Discord、KOOK 或通用 Webhook，并为游戏服务器插件提供鉴权查询接口。

- **插件标识**：`wishingskin.server-sync`
- **当前版本**：`0.1.0`
- **协议版本**：`protocol: 2`
- **入口页面**：管理端 `admin.html`

## 权限与订阅事件

- **声明权限**：`events`、`routes.public`、`routes.admin`、`frontend`、`database`、`host.public.read`、`host.users.read`
- **订阅事件**：`character.create`、`character.rename`、`character.delete`、`texture.upload`、`minecraft.link`、`minecraft.unlink`

## 接口路由

| 方法 | 路径 | 访问级别 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/query` | `public`（由 `server_api_key` 鉴权） | 游戏服务端使用预设密钥查询角色或正版绑定状态 |
| `GET` | `/status` | `admin` | 查看当前订阅配置与近期推送投递状态 |
| `POST` | `/test-notify` | `admin` | 在管理后台发送测试事件推送 |
| `POST` | `/clear-logs` | `admin` | 清理插件事件推送日志 |

## 配置项

| 键名 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `target_type` | `text` | `generic` | 推送目标格式：`generic`（通用 JSON）、`discord`（Discord Webhook）或 `kook`（KOOK 机器人） |
| `webhook_url` | `text` | `""` | 目标推送地址（HTTPS 或本机回环 HTTP） |
| `webhook_secret` | `secret` | — | 推送签名密钥（通用模式用于计算 HMAC-SHA256，KOOK 模式作为 Bot Token） |
| `kook_channel_id` | `text` | `""` | KOOK 目标频道 ID（仅选择 KOOK 时必填） |
| `enabled_events` | `text` | `""` | 允许推送的事件列表（逗号分隔），留空时推送全部 6 类已订阅事件 |
| `server_api_key` | `secret` | — | 用于 `POST /query` 鉴权的游戏服务端密钥（建议 32 位以上随机字符串） |

## 独立开发与构建

```bash
go test -count=1 ./plugins/server-sync/...
go run ./tools/pack pack -dir plugins/server-sync -out dist
```
