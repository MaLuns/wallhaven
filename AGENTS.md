# 项目约束

- UI 以 Git 原版 `wallhaven-electron` 的布局、颜色、图标和素材为准，使用 MyGO 原生控件实现；不得自行重新设计。
- macOS 开发和构建一律使用 ad-hoc 签名，禁止自动选择本机开发证书或使用本机签名私钥。
- 使用 `make dev` 和 `make build`；直接调用 MyGO CLI 时必须显式传入 `-sign -`。该版本开发命令会忽略配置中的签名身份，不能只依赖 `mygo.json`。
