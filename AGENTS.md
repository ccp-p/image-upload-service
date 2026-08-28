 # AGENTS.md — image-upload-service

 ## 项目一句话

 个人工具箱 monorepo。`cmd/` 下每个子目录是一个独立的 CLI 工具，共享同一个 Go module。部分工具有 Rust 移植版。Windows 优先。

 ## 构建

 ```powershell
 # Go 工具（通用）
 go build -o cmd/<工具名>/<工具名>.exe ./cmd/<工具名>

 # Rust 工具（hashCdn）
 cargo build --release
 ```

 网络受限时设代理：`$env:HTTP_PROXY="http://127.0.0.1:7890"; $env:HTTPS_PROXY="http://127.0.0.1:7890"`

 ## 工具索引

 | 目录 | 功能 | 语言 | 行数(估) | 测试 | 文档 |
 |------|------|------|----------|------|------|
 | hashCdn | 静态资源 hash 化 + CDN 替换 + SVN 灰度部署 | Go + Rust | Go 2400 / Rust 5600 | main_test.go | ai-workflows/AGENTS.md, README.md |
 | smartDeploy | SSH 文件同步 + 剪贴板 OTP + 文件监听 | Go | 2600+ | 14 个 _test.go | README.md |
 | bilisub | B站字幕下载 + AI 总结 | Go | 650 | 无 | agent.md |
 | douyin-dl | 抖音视频解析下载 (ABogus 签名) | Go | 1100 | 3 个 _test.go | README.md |
 | twimon | Twitter/X 用户更新监控 + PushPlus 通知 | Go | 410 | 无 | README.md |
 | cleanUnused | 清理 CSS 未引用图片 + 断链规则 | Go | 700 | 无 | README.md |
 | timeDeploy | 定时部署 (XML 解析 + HTTP 调度) | Go | 600 | main_test.go | 无 |
 | autoDlcode | 批量下载静态资源图片 | Go | 400 | 无 | 无 |
 | autoUpdatePic | 图片自动更新工具 | Go | 290 | 无 | 无 |
 | autoRetract | 群组信息提取工具 | Go | 100 | 无 | 无 |
 | downloadTg | Telegram 文件下载 (并发) | Go | 390 | 无 | 无 |
 | checkFile | 图片路径检查 + 归类 | Go | 370 | 无 | 无 |
 | testUpload | 批量打包上传 (zip 归档) | Go | 150 | 无 | 无 |
 | time | Fyne GUI 时钟 (测试 Fyne 框架) | Go | 80 | 无 | 无 |
 | cloudflared-tunnel | Cloudflare 隧道 + PushPlus 通知 | PS1 | - | test-watchdog.ps1 | 无 |
 | restartClash | 重启 Clash 代理 | Go/exe | - | 无 | 无 |

 ## 关键约定

 - **Windows 优先**：所有工具有 `.bat` 启动器，bat 内用 `chcp 65001` 确保 UTF-8。构建命令在 PowerShell 下运行。
 - **Go 零依赖原则**：bilisub 等工具只用标准库，不引入第三方依赖。新增功能应保持零依赖。hashCdn 和 smartDeploy 有少量必要依赖（fsnotify、utls 等）。
 - **双版本对齐**：hashCdn 维护 Go + Rust 双版本，共享 `version.config.json`。改一版必须同步另一版。详见 `cmd/hashCdn/ai-workflows/skills/project/dual-version-alignment/SKILL.md`。
 - **代理 7890**：本机 HTTP 代理 `http://127.0.0.1:7890`。需要联网的工具默认走这个代理。push 代码时也用此代理。
 - **状态文件**：多个工具用 `state.json` / `*.json` 保存运行状态，与代码同目录。不要清理这些文件。
 - **输出目录**：`subtitles/`（bilisub）、`D:\download\archive`（testUpload）等输出路径硬编码在代码中，修改时注意环境差异。

 ## 扩展点

 **新增 CLI 工具**：
 1. 在 `cmd/` 下新建目录，目录名即工具名
 2. 写 `main.go`（package main）
 3. 写 `.bat` 启动器（`chcp 65001` + `go build` + 运行）
 4. 工具超过 300 行或有复杂逻辑时写 `agent.md` 或 `README.md`
 5. 在本文件"工具索引"表中加一行

 **修改 hashCdn**：先读 `cmd/hashCdn/ai-workflows/AGENTS.md`，加载 dual-version-alignment 和 deploy-modes 技能。

 **修改 smartDeploy**：SSH 连接管理（keepalive → 断线重连 → OTP 等待 → 缓冲上传）是核心逻辑，改动后必须跑 `go test ./cmd/smartDeploy/`。

 **修改 bilisub**：先读 `cmd/bilisub/agent.md`。油猴脚本（`.user.js`）与 Go 服务器是两套独立代码，改 API 接口时两边都要同步。

 ## 测试

 ```powershell
 # 单个工具
 go test ./cmd/smartDeploy/
 go test ./cmd/hashCdn/
 go test ./cmd/douyin-dl/
 go test ./cmd/timeDeploy/

 # Rust
 cargo test
 ```

 ## 不要做的事

 - 不要删除根目录的 `.rs` 文件（是 hashCdn Rust 版源码）
 - 不要清理 `cmd/*/state.json` 和 `*.json` 运行状态文件
 - 不要给 bilisub 引入第三方 Go 依赖
 - 不要在 hashCdn 中只改一版不同步另一版
 - 不要在 bat 脚本里省略 `chcp 65001`
