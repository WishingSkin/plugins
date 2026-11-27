# 皮肤装扮叠层 (`wishingskin.skin-overlays`)

在保留玩家原有皮肤的前提下，叠加冬日红围巾示例或管理员上传的透明装饰层，一键实时预览、保存至个人资源库并穿戴到角色。

- **插件标识**：`wishingskin.skin-overlays`
- **当前版本**：`0.1.0`
- **协议版本**：`protocol: 2`
- **前端入口**：
  - 管理端 `admin.html`（管理预设透明装饰层）
  - 用户工作区 `studio.html`（`slot: user.tools`）
  - 皮肤卡片快捷入口 `studio.html`（`slot: texture.action`，`kinds: ["skin"]`）

## 权限声明

- `routes.user` / `routes.admin`：开放用户合成预览与管理员装饰层管理路由
- `frontend` / `user.frontend`：加载管理后台与用户前端工作室
- `database`：存储管理员上传的自定义透明装饰层
- `host.textures.read` / `host.textures.write`：读取源皮肤并保存合成结果
- `host.characters.write`：支持合成后直接穿戴至指定角色
- `host.points.spend`：结合站点积分设置按次扣费

## 接口路由

| 方法 | 路径 | 访问级别 | 说明 |
| --- | --- | --- | --- |
| `GET` | `/overlays` | `user` | 获取可用装扮叠层列表 |
| `POST` | `/preview` | `user` | 生成叠加预览图 |
| `POST` | `/apply` | `user` | 合成最终皮肤并保存/穿戴 |
| `GET` | `/admin/overlays` | `admin` | 查看已配置的装饰层 |
| `POST` | `/admin/overlays` | `admin` | 上传或新增透明装饰层 |
| `POST` | `/admin/overlays/delete` | `admin` | 删除装饰层 |

## 配置项

| 键名 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `points_cost` | `number` | `0` | 每次合成消耗积分，0 为免费（需启用站点积分功能） |
| `max_layers` | `number` | `3` | 单次最多叠加层数，有效范围 1 至 5 层 |
| `allow_public_save` | `boolean` | `false` | 是否允许用户将合成后的皮肤直接设为公开 |

## 独立开发与构建

```powershell
go test -count=1 ./plugins/skin-overlays/...
go run ./tools/pack pack -dir plugins/skin-overlays -out dist
```
