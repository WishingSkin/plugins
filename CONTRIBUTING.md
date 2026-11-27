# 贡献指南 / Contributing to WishingSkin Plugins

欢迎为 [WishingSkin/plugins](https://github.com/WishingSkin/plugins) 提交 Bug 修复、新功能、文档改进或插件体验优化！

## 目录与契约约束

- 每个插件位于 `plugins/<plugin-dir>/`，至少包含 `manifest.json`、Go 运行时入口与可选前端页面（`admin.html` / 各类 `userFrontends` 页面）。
- 所有官方插件遵循 WishingSkin Plugin Runtime v2（`protocol: 2`），宿主桥与接口规范见 [社区插件 SDK](https://github.com/WishingSkin/WishingSkin/blob/master/docs/community-plugin-sdk.md)。
- 跨插件可复用的贴图、校验与基础辅助函数统一沉淀在 `shared/`，避免重复实现。

## 本地验证命令

提交 PR 前请在仓库根目录执行以下检查：

```powershell
# 1. Go 静态检查与全仓单元测试
go vet ./...
go test -count=1 ./...

# 2. 验证打包工具（如需测试无签名打包流程）
go run ./tools/pack pack -dir plugins/<plugin> -out dist
```

## 提交与 Pull Request 检查项

1. 修改或新增配置项（`manifest.json` 的 `settings`）、路由（`routes`）、钩子（`hooks`）或事件（`events`）时，必须同步更新对应插件目录下的 `README.md`。
2. 插件前端页面通过 iframe 接入宿主，注意遵守当前主题 CSS 变量，不使用未声明的外部脚本资源，也不在请求参数中与宿主契约字段错配。
3. 出站 HTTP 请求必须走安全客户端规则（默认拦截私网、链路本地与回环重绑定，除非明确提供管理员开关且默认关闭）。
4. **多插件标签发布注意**：由于 `release-plugin.yml` 配置了 `cancel-in-progress: false` 的目录串行并发组，维护者在集中发布多个插件 Tag 时需待前一批进入队列/完成后再推送下一个 Tag，避免同一队列超过并发等待上限而被丢弃。

## 反馈分流

- **插件内部 Bug 或建议**：在本仓库 [Issues](https://github.com/WishingSkin/plugins/issues/new/choose) 选择对应模板提交。
- **宿主功能、登录认证或皮肤站本体问题**：提交至 [WishingSkin/WishingSkin](https://github.com/WishingSkin/WishingSkin/issues)。
- **安全问题**：严格通过 [SECURITY.md](SECURITY.md) 私密上报，不在公开 Issue 中披露细节。
