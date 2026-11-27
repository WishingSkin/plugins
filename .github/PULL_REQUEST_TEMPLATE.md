## 涉及插件 / Affected Plugins

- [ ] `moderation`
- [ ] `skin-overlays`
- [ ] `skin-editor`
- [ ] `skin-tools`
- [ ] `render-card`
- [ ] `server-sync`
- [ ] `ai-skin`
- [ ] `shared` / `tools/pack` / 文档与工作流

## 改动说明

- 简述本次修改的原因与方案。

## 关联 Issue

- Closes #

## 检查清单

- [ ] 已执行 `go vet ./...` 与 `go test -count=1 ./...` 且全部通过
- [ ] 若改动插件前端页面，已核对宿主桥字段名与主题 CSS 变量兼容性
- [ ] 若改动 `manifest.json`（配置项 / 权限 / 路由），已同步更新对应插件 `README.md`
- [ ] 未提交包含私钥、API Key 或 `.env.release` 的敏感文件
