# 皮肤格式与调色工具 (`wishingskin.skin-tools`)

提供经典 64×32 旧版皮肤升级、Steve / Alex 手臂模型转换、HSL 调色与透明杂点清理工具，适合批量或快速修正玩家已有皮肤。

- **插件标识**：`wishingskin.skin-tools`
- **当前版本**：`0.1.0`
- **协议版本**：`protocol: 2`
- **前端入口**：
  - 用户工作区 `workbench.html`（`slot: user.tools`）
  - 皮肤快捷动作 `workbench.html`（`slot: texture.action`，`kinds: ["skin"]`）

## 功能特性

- **64×32 升级 64×64**：自动补充新版对称手臂、双腿与第二层叠加贴图坐标。
- **Steve / Alex 手臂宽度互转**：支持 4 像素宽臂与 3 像素纤细臂双向转换。
- **HSL 调色与透明杂点清理**：微调色相、饱和度与明度，并清理非合法 UV 区域残留的孤立杂色像素。

## 权限与配置

- **声明权限**：`routes.user`、`user.frontend`、`host.textures.read`
- **路由**：`POST /transform`（`user`，在工作台提交图像与变换参数并返回处理结果）
- **后台配置项**：无（`settings: []`，安装并启用插件后即可直接使用）

## 独立开发与构建

```bash
go test -count=1 ./plugins/skin-tools/...
go run ./tools/pack pack -dir plugins/skin-tools -out dist
```
