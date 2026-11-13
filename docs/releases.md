# 自动更新与发布

应用使用 MyGO 官方原生更新插件，从 `MaLuns/wallhaven` 的最新正式 GitHub Release 检查更新。发布构建启动约 10 秒后检查，随后每天检查一次；也可通过“功能菜单 → 更新”手动检查。入口位于菜单第二行右侧，当前版本显示在“关于”窗口中。

更新窗口提供版本说明、下载进度、稍后提醒、跳过版本及安装后重启。用户可在更新窗口启用自动下载和安装，选择由 MyGO 保存在应用用户数据目录的 `updater.json` 中。开发模式不自动检查更新。

参考：[构建与分发](https://mygo.run/zh/docs/distribution)、[自动更新](https://mygo.run/zh/docs/updates)、[原生更新窗口](https://mygo.run/zh/docs/plugins/updater)。

## 首次配置更新签名

`mygo.json` 已固定更新源和 Ed25519 公钥。对应私钥为应用专属的 `mygo-update.key`，保存在维护者的用户配置目录 `mygo-wallhaven/update-keys` 下，macOS 对应 `~/Library/Application Support/mygo-wallhaven/update-keys/`。私钥权限为 `0600`，不写入项目配置，也不提交到 Git。

在 [仓库 Actions Secrets](https://github.com/MaLuns/wallhaven/settings/secrets/actions) 添加：

- 名称：`MYGO_UPDATER_PRIVATE_KEY`
- 值：`mygo-update.key` 文件的完整内容。

已登录 GitHub CLI 时也可执行：

```sh
gh secret set MYGO_UPDATER_PRIVATE_KEY --repo MaLuns/wallhaven \
  < "$HOME/Library/Application Support/mygo-wallhaven/update-keys/mygo-update.key"
```

备份这把私钥；后续发布沿用同一密钥，不要重新生成或随意替换 `updates.publicKey`。已安装的版本只接受对应私钥签名的更新。

更新包签名与系统代码签名是两套机制。macOS 构建统一使用 `-sign -` 做 ad-hoc 签名，不读取本机开发证书或代码签名私钥；Windows 开发、构建和发布不做系统代码签名，不配置 Authenticode 证书或签名命令。两端的自动更新归档均保留 Ed25519 签名校验。

## 发布流程

将代码和工作流提交到 `master` 后，在该分支的提交上创建并推送新的正式版本标签，例如：

```sh
git switch master
git pull --ff-only
git tag v1.0.1
git push origin v1.0.1
```

工作流仅接收 `v主版本.次版本.修订版本`，不接受前导零、测试版或附加后缀。标签提交必须位于远程 `master` 历史中；发布前再次校验标签仍指向本次构建的提交。旧 Electron 的 `v1.0.0` 标签已存在，应使用新版本号。

流程如下：

1. 校验标签、分支、更新源及更新签名 Secret。缺少 Secret 时直接失败。
2. 创建 Release 草稿并生成版本说明，将同一份说明写入两端更新清单。
3. 检查应用，按标签版本构建 macOS Universal（arm64 与 x86_64）及 Windows x64。
4. 使用专用私钥签名更新归档；校验归档、增量文件的大小、SHA-256 摘要对应的 Ed25519 签名、版本和下载地址。
5. 汇总两端产物，生成一份完整的 `SHA256SUMS`，上传成功后发布草稿并将其设为最新 Release。

应用版本由 CI 从标签写入构建配置，“关于”窗口显示的版本与更新清单、发布文件保持一致。

## Release 文件

| 用途 | 文件 |
| --- | --- |
| macOS 手动安装 | `mygo-wallhaven-<版本>-macos-universal.zip`，解压得到 `.app` |
| Windows 手动安装 | `mygo-wallhaven-setup-<版本>-amd64.exe`，安装到当前用户目录 |
| macOS 自动更新 | `update-darwin-universal.json`、`mygo-wallhaven-<版本>-darwin-universal.tar.gz` |
| Windows 自动更新 | `update-windows-amd64.json`、`mygo-wallhaven-<版本>-windows-amd64.tar.gz` |
| 可选增量更新 | MyGO 生成的 `*.delta`，首次发布没有此前签名归档时可不生成 |
| 安装脚本与校验 | `install.sh`、`install.ps1`、包含两端全部文件的 `SHA256SUMS` |

`SHA256SUMS` 用于检查下载完整性；应用内自动更新另用公钥验证归档签名。两端原始应用目录及私钥不会直接上传。后续版本保留早先的 Release 文件，供 MyGO 生成增量更新。

本地 `make dev` 不需要更新私钥；`make build` 没有私钥时仍可构建应用，但 MyGO 会跳过签名更新归档。CI 要求私钥存在且更新归档通过签名校验，避免把不完整的版本发布给已安装的客户端。

## 首次上线与验收

已发布的旧 Electron 客户端和未接入更新器的 MyGO 构建无法自行获得本次功能，需先手动安装首个启用自动更新的版本。之后才能通过下一次更高版本标签验收完整的“检查 → 下载 → 校验 → 安装 → 重启”流程。

应用安装位置须可写：macOS 的 `/Applications` 或 `~/Applications`，Windows 安装程序默认的当前用户目录。应用内应先结束桌面预览，再执行更新重启。

上线时仍需核对：仓库 Secret 已保存、首个带更新文件的 Release 已成功发布、macOS 和 Windows 均完成从旧版更新到新版的真实验收。代码与本地打包验证不能代替这三项外部状态。
