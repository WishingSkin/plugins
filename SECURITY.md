# 安全政策 / Security Policy

## 报告安全漏洞

如果你在 `WishingSkin/plugins` 仓库内的任一官方插件、`tools/pack` 打包工具或市场目录机制中发现安全漏洞，请**不要**在公开 Issue 或 Pull Request 中直接披露细节。

请通过以下私密渠道提交报告：

- 前往 [GitHub Private Vulnerability Reporting](https://github.com/WishingSkin/plugins/security/advisories/new) 提交漏洞详情。

提交时建议包含：

1. 受影响的插件 ID 与版本（或 commit SHA），以及宿主 WishingSkin 版本。
2. 复现步骤、触发配置与影响范围说明。
3. 如涉及修复建议或补丁思路，欢迎一并附上。

## 插件安全模型与运行边界

- **官方签名校验**：官方发布流水线使用独立的 Ed25519 密钥对插件包产物签名，`WishingSkin v0.3.0+` 已内置对应公钥。用户或站长安装第三方未签名插件前须自行评估来源可信度。
- **本机运行信任边界**：Plugin Runtime v2 动态插件以本机可执行子进程方式运行，通过 manifest 声明权限（如 `database`、`host.textures.write`、`hooks`、`routes.*`）并由宿主按权限边界转发 Host API，但不提供操作系统级强制沙箱。
- **外部连接控制**：涉及外部 Webhook、图像推理节点或审核接口的插件，默认仅允许公网 HTTPS 或明确受限的回环地址；内网或局域网地址访问受插件显式开关与宿主出站规则约束。
