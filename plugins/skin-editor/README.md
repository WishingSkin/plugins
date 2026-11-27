# 在线皮肤编辑器 (`wishingskin.skin-editor`)

提供带 UV 分区标注网格、对称绘制与实时预览的 64×64 Minecraft 皮肤像素工作台，无需安装第三方软件即可在浏览器内精细修改皮肤。

- **插件标识**：`wishingskin.skin-editor`
- **当前版本**：`0.1.0`
- **协议版本**：`protocol: 2`
- **前端入口**：
  - 用户工作区 `editor.html`（`slot: user.tools`）
  - 皮肤快捷动作 `editor.html`（`slot: texture.action`，`kinds: ["skin"]`，可直接载入当前皮肤编辑）

## 功能特性

- 64×64 标准及 64×32 布局 UV 分区引导与画布绘制。
- 通过用户工作区直接保存修改后的皮肤到个人衣柜或直接应用到角色（通过宿主授权桥完成）。
- 内置基础皮肤底版模板列表，方便快速起稿或二次微调。

## 权限与配置

- **声明权限**：`routes.user`、`user.frontend`
- **路由**：`GET /templates`（`user`，返回内置编辑模板）
- **后台配置项**：无（`settings: []`，安装并启用插件后即可直接使用）

## 独立开发与构建

```powershell
go test -count=1 ./plugins/skin-editor/...
go run ./tools/pack pack -dir plugins/skin-editor -out dist
```
