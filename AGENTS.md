# 项目约束

- UI 以 Git 原版 `wallhaven-electron` 的布局、颜色、图标和素材为准，使用 MyGO 原生控件实现；不得自行重新设计。
- macOS 开发和构建一律使用 ad-hoc 签名，禁止自动选择本机开发证书或使用本机签名私钥。
- Windows 开发、构建和发布不做系统代码签名，不配置 Authenticode 证书或签名命令；自动更新归档保留独立的 Ed25519 签名校验。
- 使用 `make dev` 和 `make build`；直接调用 MyGO CLI 时必须显式传入 `-sign -`。该版本开发命令会忽略配置中的签名身份，不能只依赖 `mygo.json`。
- 仅在用户明确要求时修改 `README.md`；多代理协作时必须指定单一负责者，禁止并发修改该文件。
- 发布前必须在版本号、更新日志、中英文目录及生成文件全部更新后，对最终待打标签内容执行完整检查：`CGO_ENABLED=0 WALLHAVEN_LIVE_TEST=0 go test ./...`、`CGO_ENABLED=0 go test ./.github/scripts/releasecheck`、`go tool mygo vet .` 和 `make build`。不得使用版本更新前的结果或局部测试代替最终版本验证。
- 打标签前还必须对同一份最终内容执行 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`，覆盖 Windows 专用 Go 源文件。`v1.1.3` 的 Windows CI 在桌面预览回调中发现两处 `uintptr` 转 `unsafe.Pointer` 的问题，macOS 宿主的 vet 未检查这些文件。
- 将最终发布提交推送到 `master` 后，在 GitHub Actions 运行 `Release preflight`。确认同一个提交的 `release-preflight/verified` 状态成功后，才可创建并推送正式发布标签。发布内容有变化时，必须对新提交重新预检。
- 排查 CI 失败必须读取目标 run/job 的失败步骤原始日志，确认具体失败命令、测试及首个错误后再判断原因；本地通过不能代替 macOS、Windows CI 结果。日志读取受限时必须明确原因尚未确认，先解决访问或取得错误日志，禁止将猜测写成故障原因或防复发规则。
- UI 自动化测试必须在测试入口显式固定基准语言 `zh-Hans`，不得依赖开发机或 CI 的系统语言；英文及跟随系统的专项测试须显式切换语言并在清理时恢复，不能修改真实系统或用户设置。`v1.1.1` 的 Windows CI 曾因界面为英文而测试查找中文标签失败（如“前往Featured”“功能菜单”）；新增多语言或调整语言初始化后，须分别在 `MYGO_LOCALE=zh-Hans` 和 `MYGO_LOCALE=en` 下运行 UI 测试，验证宿主语言不会污染通用测试。
