# WishingSkin 官方插件仓库

本仓库维护 [WishingSkin](https://github.com/WishingSkin/WishingSkin) 的官方外置扩展插件与静态市场索引。

## 官方市场源

WishingSkin 支持在管理后台“插件管理 → 插件市场源”订阅静态 JSON 目录，核验 SHA-256 摘要并一键安装、升级插件包：

```text
https://wishingskin.github.io/plugins/catalog.json
```

> 若暂未启用 GitHub Pages，也可直接使用对应分支 Raw 文件地址：
> `https://raw.githubusercontent.com/WishingSkin/plugins/main/catalog/catalog.json`

## 仓库结构

```text
.
├── catalog/
│   └── catalog.json       # 静态市场源索引（packages[]）
├── plugins/               # 各官方插件源码与清单目录
├── shared/                # 跨插件共享工具库（如 UV 贴图处理、公共校验）
├── tools/pack/            # 官方插件打包与市场清单更新工具
└── .github/workflows/     # CI、按 Tag 交叉打包发布与 Pages 部署流水线
```

## 插件规划

| 目录 | 插件标识 | 定位 | 状态 |
| --- | --- | --- | --- |
| `plugins/moderation` | `wishingskin.moderation` | 合规校验与内容审核（敏感词、规则过滤与外部审核接口） | `v0.1.0` 已实现 |
| `plugins/skin-overlays` | `wishingskin.skin-overlays` | 皮肤装饰层（圣诞装、万圣节饰品等透明叠层一键合成） | `v0.1.0` 已实现 |
| `plugins/skin-editor` | `wishingskin.skin-editor` | 在线皮肤编辑器（UV 分区标注与 2D 像素绘制及实时预览） | `v0.1.0` 已实现 |
| `plugins/skin-tools` | `wishingskin.skin-tools` | 手臂模型转换、64×32 升级与调色工具 | `v0.1.0` 已实现 |
| `plugins/render-card` | `wishingskin.render-card` | 头像、全身渲染图与玩家 SVG 名片端点 | `v0.1.0` 已实现 |
| `plugins/server-sync` | `wishingskin.server-sync` | 外部社群与服务器生态联动 | `v0.1.0` 已实现 |
| `plugins/ai-skin` | `wishingskin.ai-skin` | AI 皮肤工坊（纯文本 D2-Strict 精绘、图像 B2-v2 投影与外部推理节点） | `v0.1.0-beta.1` 测试版 |

## 本地打包与检查

```powershell
# 运行仓库单元测试与静态检查
go vet ./...
go test -count=1 ./...

# 生成 Ed25519 官方签名密钥对
go run ./tools/pack keygen

# 交叉编译并生成可导入 WishingSkin 后台的签名 ZIP 插件包
go run ./tools/pack pack -dir plugins/<plugin> -out dist -sign-key <seed> -require-sign

# 校验插件包官方签名
go run ./tools/pack verify -file dist/<plugin>-<version>.zip -pubkey <pub>
```

## 发布规范

每个插件独立使用 `<plugin-dir>-v<semver>` 格式 Git Tag（例如 `moderation-v0.1.0`）触发 `.github/workflows/release-plugin.yml`，使用仓库 Secret `WS_PLUGIN_SIGNING_KEY` 对官方 `wishingskin.*` 插件包进行 Ed25519 签名、构建多平台 ZIP 包、计算 SHA-256、发布 GitHub Release 并更新 `catalog/catalog.json`。
