<p align="center">
  <img src="https://raw.githubusercontent.com/WishingSkin/WishingSkin/master/docs/assets/logo.svg" alt="WishingSkin Logo" width="96" height="96" />
</p>

<h1 align="center">WishingSkin 官方插件仓库</h1>

<p align="center">
  [WishingSkin](https://github.com/WishingSkin/WishingSkin) 官方 Plugin Runtime v2 动态插件与静态市场源
</p>

<p align="center">
  <a href="https://github.com/WishingSkin/plugins/actions/workflows/ci.yml"><img src="https://github.com/WishingSkin/plugins/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/WishingSkin/plugins/actions/workflows/pages.yml"><img src="https://github.com/WishingSkin/plugins/actions/workflows/pages.yml/badge.svg" alt="Deploy Catalog Pages" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/WishingSkin/plugins" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8" alt="Go 1.26" />
  <a href="https://wishingskin.github.io/plugins/catalog.json"><img src="https://img.shields.io/badge/Catalog-Online-2E8540" alt="Official Catalog" /></a>
</p>

---

本仓库维护 [WishingSkin](https://github.com/WishingSkin/WishingSkin) 的官方外置扩展插件、打包签名工具与静态市场索引。所有插件基于 Plugin Runtime v2（protocol 2）构建，由 GitHub Actions 自动交叉编译并使用官方 Ed25519 私钥签名发布。

## 一键订阅与安装

自 `WishingSkin v0.3.0` 起，宿主已内置官方 Ed25519 验签公钥，无需手动配置环境变量。管理员进入「管理后台 → 插件管理 → 插件市场」，点击「填入官方市场源」（或手动填入下方地址）即可在线校验 SHA-256 摘要与官方签名后安装：

```text
https://wishingskin.github.io/plugins/catalog.json
```

> 若 GitHub Pages 访问受限，可使用仓库默认分支的 Raw 备用地址：
> `https://raw.githubusercontent.com/WishingSkin/plugins/main/catalog/catalog.json`

安装具体流程及动态插件沙箱边界见 [社区插件 SDK 手册](https://github.com/WishingSkin/WishingSkin/blob/master/docs/community-plugin-sdk.md)。

## 官方插件目录

| 目录 | 插件标识 | 当前版本 | 定位与说明 |
| --- | --- | --- | --- |
| [moderation](plugins/moderation/README.md) | `wishingskin.moderation` | `0.1.0` | 合规校验与内容审核（敏感词、RE2 正则、空白/纯色图拦截与外部审核 Webhook） |
| [skin-overlays](plugins/skin-overlays/README.md) | `wishingskin.skin-overlays` | `0.1.0` | 皮肤装扮叠层（冬日围巾等透明装饰层叠加、实时预览与一键穿戴） |
| [skin-editor](plugins/skin-editor/README.md) | `wishingskin.skin-editor` | `0.1.0` | 在线皮肤编辑器（UV 分区标注网格、对称绘制与 64×64 实时预览工作台） |
| [skin-tools](plugins/skin-tools/README.md) | `wishingskin.skin-tools` | `0.1.0` | 皮肤格式与调色工具（64×32 升级、Steve/Alex 手臂转换、HSL 调色与杂点清理） |
| [render-card](plugins/render-card/README.md) | `wishingskin.render-card` | `0.1.0` | 角色渲染与玩家名片（公开头像、全身渲染图与可嵌入 README 的 SVG 名片） |
| [server-sync](plugins/server-sync/README.md) | `wishingskin.server-sync` | `0.1.0` | 社群与游戏服务器联动（Discord / KOOK / 通用事件推送与服务端鉴权查询） |
| [ai-skin](plugins/ai-skin/README.md) | `wishingskin.ai-skin` | `0.1.0-beta.1` | AI 皮肤工坊测试版（文本 D2-Strict 像素精绘、图像 B2-v2 投影与外部算力节点） |

## 仓库结构

```text
.
├── catalog/
│   └── catalog.json       # 静态市场源索引（packages[]）
├── plugins/               # 各官方插件源码、前端页面与 manifest.json
├── shared/                # 跨插件共享工具库（如 UV 贴图处理、公共校验）
├── tools/pack/            # 官方插件打包、签名与市场索引更新工具
└── .github/workflows/     # CI、按 Tag 交叉打包发布与 Pages 部署流水线
```

## 本地打包与检查

需要 Go `1.26.11`（见 `go.mod`）：

```powershell
# 运行仓库静态检查与单元测试
go vet ./...
go test -count=1 ./...

# 生成 Ed25519 官方签名密钥对
go run ./tools/pack keygen

# 交叉编译并生成可导入 WishingSkin 后台的签名 ZIP 插件包
go run ./tools/pack pack -dir plugins/<plugin> -out dist -sign-key <seed> -require-sign

# 校验插件包官方签名
go run ./tools/pack verify -file dist/<plugin>-<version>.zip -pubkey <pub>
```

## 发布规范与贡献

- **版本发布**：每个插件独立使用 `<plugin-dir>-v<semver>` 格式 Git Tag（例如 `moderation-v0.1.0`、`ai-skin-v0.1.0-beta.1`）触发 `.github/workflows/release-plugin.yml`，由仓库 Secret `WS_PLUGIN_SIGNING_KEY` 完成多平台编译、Ed25519 签名、Release 发布与 `catalog/catalog.json` 更新。版本号含 `-` 时自动标记为 Pre-release。
- **参与贡献**：提 Issue 或 PR 前请阅读 [贡献指南](CONTRIBUTING.md)；宿主相关 Bug 请前往 [WishingSkin/WishingSkin](https://github.com/WishingSkin/WishingSkin) 提交。
- **安全反馈**：发现插件权限越界、SSRF 或签名验证漏洞，请通过 [SECURITY.md](SECURITY.md) 中的私密渠道上报。
- **许可证**：采用 [MIT 许可证](LICENSE)。
