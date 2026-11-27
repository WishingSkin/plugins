# 角色渲染与玩家名片 (`wishingskin.render-card`)

提供角色头像、全身渲染图与玩家 SVG 名片公开端点，并在用户端提供可视化名片与 Markdown 嵌入链接生成器。

- **插件标识**：`wishingskin.render-card`
- **当前版本**：`0.1.0`
- **协议版本**：`protocol: 2`
- **前端入口**：用户工作区 `studio.html`（`slot: user.tools`）

## 权限声明

- `routes.public` / `routes.user`：暴露免登录公开渲染端点与当前登录用户预览信息接口
- `user.frontend`：加载用户端「玩家名片与渲染图」工作台
- `host.public.read`：读取公开角色名称、UUID 与已绑定公开皮肤材质

## 接口路由

| 方法 | 路径 | 访问级别 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/avatar` | `public` | 返回角色 2D 头像图像 |
| `GET` | `/body` | `public` | 返回角色全身正面渲染图 |
| `GET` | `/card` | `public` | 返回可嵌入论坛或 README 的玩家 SVG 名片 |
| `GET` | `/info` | `user` | 返回当前登录用户的可用角色列表与拼装好的公开链接预览 |

## 配置项

| 键名 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `cache_max_age` | `number` | `300` | 公开图像 `Cache-Control max-age` 秒数，允许范围 0 至 86400 秒 |
| `default_theme` | `text` | `light` | URL 未指定 `theme` 时默认配色风格：`light`、`dark` 或 `emerald` |
| `show_verified_badge` | `boolean` | `true` | 是否在玩家名片中展示正版 Minecraft 验证徽标 |

## 独立开发与构建

```bash
go test -count=1 ./plugins/render-card/...
go run ./tools/pack pack -dir plugins/render-card -out dist
```
