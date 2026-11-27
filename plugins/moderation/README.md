# 合规校验与内容审核 (`wishingskin.moderation`)

为角色名、昵称与材质上传提供同步敏感词、RE2 正则表达式、图像健康度检测与外部审核 Webhook 校验。

- **插件标识**：`wishingskin.moderation`
- **当前版本**：`0.1.0`
- **协议版本**：`protocol: 2`
- **入口页面**：管理端 `admin.html`

## 功能特性

- **同步文本拦截钩子 (`text.check`)**：支持敏感词表、人工复核词表与最多 32 条 RE2 正则规则，可配置命中后直接拒绝或转入人工审核。
- **材质图像检测 (`texture.upload.check`)**：支持拦截有效像素过少的透明空白材质，以及将纯单色材质自动转入人工审核队列。
- **外部审核 Webhook**：支持对接 HTTPS 或本机回环 HTTP 审核接口，可配置 Bearer 密钥、异常回退策略（放行 / 转人工 / 拒绝）及是否附带上传图片 Base64。
- **后台测试与日志排查**：管理页支持查看拦截记录、触发测试校验及清理记录。

## 权限声明

- `hooks`：订阅同步文本校验钩子
- `hooks.image`：在上传审核钩子中读取图像像素数据
- `routes.admin`：暴露管理端状态、测试与清理日志接口
- `frontend`：挂载管理后台 `admin.html` 面板
- `database`：持久化拦截统计与审核日志

## 路由与钩子

| 类型 | 路径 / 名称 | 访问级别 | 说明 |
| --- | --- | --- | --- |
| Hook | `text.check` | 宿主同步调用 | 校验角色名与文本输入 |
| Hook | `texture.upload.check` | 宿主同步调用 | 校验上传皮肤与披风图像内容 |
| Route | `GET /status` | `admin` | 获取审核规则状态与近期记录 |
| Route | `POST /test` | `admin` | 在后台测试文本与规则命中情况 |
| Route | `POST /clear-logs` | `admin` | 清空插件审核记录 |

## 配置项

| 键名 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `mode` | `text` | `block` | 命中处理模式：`block`（直接拒绝）或 `review`（材质转人工审核） |
| `blocked_words` | `text` | `""` | 敏感词列表，支持逗号或换行分隔 |
| `review_words` | `text` | `""` | 人工复核词列表，支持逗号或换行分隔 |
| `regex_rules` | `text` | `""` | 正则拦截规则，每行一条 RE2 表达式，最多 32 条 |
| `reject_blank_skin` | `boolean` | `false` | 是否拦截有效像素过少的透明空白材质 |
| `reject_solid_skin` | `boolean` | `false` | 是否将纯单色材质自动转入人工审核 |
| `webhook_url` | `text` | `""` | 外部内容审核接口 URL（HTTPS 或本机回环 HTTP） |
| `webhook_secret` | `secret` | — | 外部审核接口 Bearer Token 鉴权密钥 |
| `webhook_fail_policy` | `text` | `allow` | 外部接口异常回退策略：`allow`、`review` 或 `reject` |
| `webhook_send_image` | `boolean` | `false` | 调用外部审核接口时是否附带上传图片 Base64 |

## 独立开发与构建

```powershell
go test -count=1 ./plugins/moderation/...
go run ./tools/pack pack -dir plugins/moderation -out dist
```
