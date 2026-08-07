# 技能：Go/Rust 双版本对齐

## 适用场景

当修改 hashCdn 的任何行为逻辑、正则匹配、CLI 参数、配置字段时，必须同时保持 Go 版（main.go）和 Rust 版（*.rs）的一致性。

## 核心规则

### 1. 修改任何一版，必须检查另一版

Go 版入口：`cmd/hashCdn/main.go`（单文件，~2400 行）
Rust 版模块拆分：
- `config.rs` — 配置结构 + JSON 加载（对应 Go 的 `Config`/`DeployConfig` + `loadConfig`）
- `patterns.rs` — 正则/字符串匹配（对应 Go 的包级 `regexp` 变量 + `getRegex` 缓存）
- `version_manager.rs` — 版本管理器（对应 Go 的 `VersionManager`）
- `deploy.rs` — 部署管理器 + 缓存（对应 Go 的 `DeployManager` + `DeployCache`）
- `json.rs` — 手写 JSON parser（零依赖，Rust 独有）
- `md5.rs` — MD5 实现（Rust 独有）
- `main_new.rs` — CLI 入口（对应 Go 的 `main()`）

### 2. 对应关系速查表

| 功能 | Go 位置 | Rust 位置 |
|------|---------|-----------|
| 配置结构 | `Config`/`DeployConfig` 结构体 | `config.rs` |
| 加载配置 | `loadConfig()` | `config.rs` |
| 包级正则 | `var ()` 块 | `patterns.rs` 函数 |
| 正则缓存 | `getRegex()` + `sync.Map` | `patterns.rs`（无缓存，直接调用） |
| hash 文件名 | `addHashToFilename`/`removeHashFromFilename` | `patterns.rs` |
| CSS url() | `reCSSUrlCollect`/`reCSSUrlReplace` | `collect_css_urls`/`replace_css_urls` |
| HTML link/script | `reHTMLCSSLink`/`reHTMLJSScript` | `collect_html_links`/`collect_html_scripts` |
| HTML 注释 | `reHTMLComment` | `remove_html_comments` |
| 处理 HTML | `processHTMLFile` | `version_manager.rs` |
| 部署执行 | `DeployManager.Run` | `deploy.rs` |
| CDN 校验 | `validateCDNResources` | `deploy.rs` |
| SVN 操作 | `vcsSvnDelete`/`svnCommit` | `deploy.rs` |
| 部署缓存 | `DeployCache` | `deploy.rs` |
| CLI 参数 | `main()` 中 `flag.*` | `main_new.rs` 手写解析 |

### 3. 正则对齐（最易遗漏）

Go 用 `regexp` 包，Rust 用 `patterns.rs` 中的手写字符串匹配。两者必须行为一致。

关键正则对照：
- `^(.+)\.([a-f0-9]{4,64})\.(css|js|jpg|...)$` -> `parse_hashed_filename()`
- `\.[a-f0-9]{4,64}$` -> `remove_hash_suffix()`
- `url\(\s*(['"]?)([^'")\s]+)(['"]?)\s*\)` -> `collect_css_urls()`
- `<link[^>]*href\s*=\s*['"]([^'"]+\.css...)['"]` -> `collect_html_links(html, "css")`
- `<script[^>]*src\s*=\s*['"]([^'"]+\.js...)['"]` -> `collect_html_scripts(html)`
- `(?s)<!--.*?-->` -> `remove_html_comments(content)`

### 4. 配置字段对齐

两版共享 `version.config.json`。新增配置字段时：
- Go: 在 `Config` 或 `DeployConfig` 结构体加字段 + JSON tag
- Rust: 在 `config.rs` 对应结构体加字段 + 反序列化逻辑
- JSON tag 名称必须完全一致

### 5. CLI 参数对齐

两版 CLI 参数必须一致（Rust 额外支持 `-revert-git`）。
新增参数时：
- Go: `flag.String/Bool/Int` + 在 `main()` 中处理
- Rust: `main_new.rs` 手写解析 + 在 `main()` 中处理

## 检查清单

修改代码后逐项确认：

- [ ] 如果改了 Go 的正则，Rust 的 `patterns.rs` 对应函数是否同步修改？
- [ ] 如果改了 Rust 的 `patterns.rs`，Go 的包级正则变量是否同步修改？
- [ ] 如果新增了配置字段，两版的 `Config` 结构体是否都加了？JSON tag 是否一致？
- [ ] 如果新增了 CLI 参数，两版的 `main()` 是否都处理了？
- [ ] 如果改了 hash 处理逻辑，两版的 `renameFileWithHash`/对应 Rust 函数行为是否一致？
- [ ] 如果改了部署逻辑，两版的 `DeployManager.Run`/对应 Rust 函数行为是否一致？
- [ ] 如果改了部署模式常量（ModePreScriptCopy 等），两版是否同步？
- [ ] 修改后是否跑了 `go test -v`？

## 测试验证

```powershell
cd D:\project\my_go_project\image-upload-service\cmd\hashCdn
go test -v
```

Rust 版测试依赖 `patterns.rs` 中的 `#[cfg(test)]` 模块：
```powershell
cd D:\project\my_go_project\image-upload-service
cargo test
```
