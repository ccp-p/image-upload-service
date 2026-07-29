# hashCdn / hash-cdn Agent Guide

静态资源 hash 版本化 + CDN 替换 + SVN 灰度部署工具。服务于中国移动 xhmqqthy 项目的 H5 页面（xdrNormal.html）及其组件（xdrsign、xdrsignNew、xdrInvite）。

Go 版和 Rust 版功能等价，共享同一份 `version.config.json` 和工作目录。Rust 版为纯 std-lib 零依赖重写，适合低配设备。

## 项目布局

```
image-upload-service/                 Go module: image-upload-service
├── go.mod / go.sum
├── Cargo.toml / Cargo.lock           Rust crate: hash-cdn (bin: hash-cdn)
├── config.rs                         Rust: 配置结构 + JSON 解析
├── deploy.rs                         Rust: 部署管理器
├── json.rs                           Rust: 手写 JSON parser (零依赖)
├── md5.rs                            Rust: MD5 实现
├── patterns.rs                       Rust: 正则编译缓存
├── version_manager.rs                Rust: 版本管理器
├── main_new.rs                       Rust: 入口 (bin path)
├── target/release/hash-cdn.exe       编译后的 Rust 二进制
└── cmd/hashCdn/                      <- 当前工作目录
    ├── main.go                       Go 版全部实现 (~2386 行单文件)
    ├── main_test.go                  Go 版测试
    ├── version.config.json           配置文件 (双版本共用)
    ├── run_hash_cdn.bat              Go 版交互式运行脚本
    ├── run_hash_cdn_rs.bat           Rust 版交互式运行脚本
    ├── hashCdn.exe                   编译后的 Go 二进制
    ├── .deploy-cache.json            部署文件 hash 缓存 (持久化)
    ├── .run_cache.ini                上次选择的部署模式
    ├── .version-map.json             版本映射输出
    └── README.md                     用户文档
```

## 核心概念

**双环境**: 通过环境变量 `IS_HOME` 区分家里/公司电脑，自动选择不同的源路径、目标路径和 HTML 文件路径。`IS_HOME=1` 为家里，`IS_HOME=0` 或未设置为公司（默认）。

**源路径 (source)**: git 仓库 `richinfo_tyjf_xhmqqthy/src/main/webapp/res/wap`，开发改代码的地方。

**目标路径 (dest)**: SVN 工作副本 `huidu/xhmqqthy-res`，灰度发布目录，文件复制到这里后提交 SVN。

**组件 (components)**: `includeComponents` 配置项指定只处理哪些组件（如 `xdrInvite`、`xdrsign`、`xdrsignNew`）。未配置则处理全部。

**主资源 (main resources)**: `processMainResources` 指定哪些 HTML 文件需要对其主 JS/CSS（与 HTML 同名的 .js/.css）做 hash 处理。未在列表中的 HTML 只处理组件资源，跳过主 JS/CSS。

**CDN 排除 (cdnExcludeFiles)**: 列表中的文件不做 CDN 替换，保持相对路径。对应 bat 脚本里的"盲盒组件"。

## 工作流程

1. **Hash 版本化**: 对 HTML 引用的 JS/CSS/图片计算 MD5，生成带 hash 的文件副本（如 `style.abc12345.css`），删除旧 hash 文件，更新 HTML 和 CSS 中的引用路径。
2. **CDN 替换**: 可选地将 HTML 中的相对路径替换为 CDN 域名前缀（`https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap`）。
3. **部署复制**: 将源路径中的文件（含 hash 版本）复制到目标路径，用 `.deploy-cache.json` 缓存避免重复计算 MD5。
4. **CDN 校验**: 部署后校验 HTML 中引用的 CDN 资源是否都已在目标目录就绪（忽略 HTML 注释中的引用）。
5. **SVN 提交**: 可选地自动 `svn commit`，提交信息取自 Git 最新 commit 或用户自定义。
6. **HTML 回滚 + Git 提交**: 可选地用 `git checkout HEAD --` 回滚 HTML 到 CDN 替换前的状态，然后 `git add -A && git commit && git pull --rebase && git push`。

## 配置文件 version.config.json

| 字段 | 类型 | 说明 |
|------|------|------|
| `rootDir` | string | 项目根目录，默认 `.` |
| `cdnDomain` | string | CDN 域名前缀，留空则保持相对路径 |
| `hashLength` | int | hash 截取长度，默认 8 |
| `homeHTMLFile` / `companyHTMLFile` | string | 按环境选择的 HTML 文件路径 |
| `includeComponents` | []string | 只处理这些组件 |
| `processMainResources` | []string | 这些 HTML 的主 JS/CSS 也做 hash |
| `excludeDirs` | []string | 扫描时跳过的目录 |
| `cdnExcludeFiles` | []string | 不做 CDN 替换的文件 |
| `deploy.enabled` | bool | 是否启用部署 |
| `deploy.command` | string | `copy` 或 `copy-commit` |
| `deploy.autoCommit` | bool | 是否自动提交 SVN |
| `deploy.homeSourcePath` / `companySourcePath` | string | 源路径 (git 仓库) |
| `deploy.homeDestPath` / `companyDestPath` | string | 目标路径 (SVN 灰度目录) |
| `deploy.filePaths` | []string | 要复制的文件 glob 模式 |
| `deploy.cdnPathPrefix` | string | CDN URL 中需裁掉的前缀，用于映射到 dest 本地路径 |
| `deploy.gitAuthors` | []string | Git 作者过滤 |
| `deploy.homeNodeScript` / `companyNodeScript` | string | Node.js 部署脚本路径 (前置脚本) |
| `rollbackAfterDeploy` | bool | 部署后回滚 HTML |
| `gitCommitAfterRollback` | bool | 回滚后执行 git commit & push |

## CLI 参数

Go 版和 Rust 版参数一致（Rust 额外支持 `-revert-git`）:

```
-config <path>           配置文件路径 (默认 version.config.json)
-file <path>             单个 HTML 文件 (优先级高于配置)
-all                     扫描所有 HTML 文件
-cdn <domain>            覆盖 CDN 域名
-debug                   调试模式 (详细日志)
-deploy                  仅部署，不处理 hash
-deploy-commit           部署并自动提交 SVN
-mode <1-9>              部署模式 (见下表)
-message <text>          自定义 SVN 提交信息
-revert-svn              回退 dest SVN 工作副本的所有本地变更
-revert-git              回退 src git 的所有本地变更 (仅 Rust)
-dry-run                 预览模式，不实际修改文件
```

## 部署模式 (mode 1-9)

三个维度的组合: CDN 行为 x 提交行为 x 回滚行为。

| mode | CDN 行为 | 提交 | 回滚HTML + git push |
|------|----------|------|---------------------|
| 1 | 替换CDN | copy | 否 |
| 2 | 替换CDN | copy-commit | 否 |
| 3 | 替换CDN | copy-commit | 是 |
| 4 | 保持相对路径 | copy | 否 |
| 5 | 保持相对路径 | copy-commit | 否 |
| 6 | 保持相对路径 | copy-commit | 是 |
| 7 | 排除盲盒组件后替换CDN | copy | 否 |
| 8 | 排除盲盒组件后替换CDN | copy-commit | 否 |
| 9 | 排除盲盒组件后替换CDN | copy-commit | 是 |

bat 脚本额外提供: mode 10 = 仅部署 copy，11 = 仅部署 copy-commit，12 = 回退 SVN (Rust: 13 = 回退 SVN+git，14 = 仅回退 git)。

## Go 版代码结构 (main.go)

单文件，主要类型:

- **`Config` / `DeployConfig`**: 配置结构体，从 JSON 反序列化。
- **`VersionManager`**: 版本管理器。核心方法:
  - `processHTMLFile` — 处理单个 HTML 及其关联资源
  - `renameFileWithHash` — 计算文件 MD5，生成 hash 文件名（保留原文件）
  - `findAndDeleteOldHashFiles` — 删除目录下同 basename 的旧 hash 文件
  - `collectResourcesFromHTML` — 正则扫描 HTML 中的 `<link href>` 和 `<script src>`
  - `processComponentCSS` — 处理组件 CSS，包括其内部 `url()` 引用的图片
  - `updateHTMLContent` — 更新 HTML 中的资源引用为 hash 版路径
  - `shouldExcludeFromCDN` — O(1) map 查找 CDN 排除文件
  - `rollbackHTMLFile` — `git checkout HEAD -- <file>` 回滚 HTML
  - `gitCommitAndPushAfterRollback` — 全量 git add/commit/pull --rebase/push
- **`DeployManager`**: 部署管理器。核心方法:
  - `Run` — 执行复制 + CDN 校验 + SVN 提交
  - `copyFileWithVersions` — 复制文件及其所有 hash 版本到 dest
  - `cleanHashFiles` — 清理 dest 中的旧 hash 文件
  - `validateCDNResources` — 校验 HTML 中 CDN 资源在 dest 存在
  - `svnCommit` — 执行 svn commit
  - `revertAllSvn` — 递归 svn revert
- **`DeployCache`**: 持久化 hash 缓存。用 size + modTime 判断文件是否变化，未变则复用缓存的 MD5，避免重复 IO。缓存在 `.deploy-cache.json`。
- **辅助函数**: `vcsSvnDelete` (`svn delete --keep-local`)、`vcsGitAdd` (`git add`)、`isHomeEnv`、`isJSOrCSS`、`getRegex` (正则缓存 `sync.Map`)。

关键正则（包级编译，全局复用）:
- `reHashInFilename` — 匹配 `name.hash.ext`
- `reCSSUrlCollect` / `reCSSUrlReplace` — CSS `url()` 提取和替换
- `reHTMLCSSLink` / `reHTMLJSScript` — HTML 中 CSS/JS 引用
- `reHTMLComment` — HTML 注释（校验 CDN 时先移除注释）

## Rust 版代码结构

与 Go 版一一对应，拆分为多个模块:

- `config.rs` — `Config` / `DeployConfig` 结构体 + JSON 加载
- `json.rs` — 手写 JSON parser（零外部依赖）
- `md5.rs` — MD5 实现
- `patterns.rs` — 正则编译缓存（对应 Go 的 `regexCache`）
- `version_manager.rs` — `VersionManager`
- `deploy.rs` — `DeployManager` + `DeployCache`
- `main_new.rs` — 入口，CLI 参数解析（手写，支持 `-flag value` 和 `-flag=value`）

## 构建与运行

Go 版:
```powershell
cd D:\project\my_go_project\image-upload-service\cmd\hashCdn
go build -o hashCdn.exe main.go
# 或直接运行:
go run main.go -config=version.config.json -mode=6
```

Rust 版:
```powershell
cd D:\project\my_go_project\image-upload-service
cargo build --release
# 二进制在 target\release\hash-cdn.exe
```

测试:
```powershell
cd D:\project\my_go_project\image-upload-service\cmd\hashCdn
go test -v
```

交互式运行（推荐用户使用）:
- `run_hash_cdn.bat` — Go 版，中文交互菜单
- `run_hash_cdn_rs.bat` — Rust 版，英文交互菜单

两个 bat 脚本都会: 检查 `IS_HOME` 环境变量切换路径、读取 `.run_cache.ini` 记住上次模式、对需要提交的模式提示输入 commit message、缓存模式到 `.run_cache.ini`。

## 开发约定

- **单文件 Go**: Go 版全部逻辑在 `main.go`，不拆包。新功能直接加在这个文件里。
- **Rust 多文件**: Rust 版按模块拆分，每个 `.rs` 文件对应一个职责。
- **功能对齐**: 修改 Go 版后应同步更新 Rust 版，反之亦然。两版共享 `version.config.json`，配置字段必须保持一致。
- **保留原文件**: hash 处理永远生成副本，不删除原始无 hash 文件。`renameFileWithHash` 是 copy 语义。
- **清理旧 hash**: 新 hash 文件生成后，自动删除同 basename 的旧 hash 文件（`findAndDeleteOldHashFiles` 在源目录，`cleanHashFiles` 在目标目录）。
- **正则缓存**: 动态生成的正则用 `getRegex` + `sync.Map` 缓存，避免重复编译。
- **CDN 校验**: 校验前先移除 HTML 注释 `<!-- -->`，避免注释中的 URL 导致误报。
- **环境路径**: 所有硬编码路径在 `loadConfig` 中根据 `IS_HOME` 选择 home/company 版本。新增路径配置时需同时加 home 和 company 两个字段。
- **部署缓存**: `.deploy-cache.json` 记录每个文件的 hash + size + modTime，文件未变时跳过 MD5 计算。修改文件内容后 modTime 变化，缓存自动失效。
- **编码**: bat 脚本和程序输出使用 UTF-8 (`chcp 65001`)。代码中含中文日志和 emoji。
- **`.gitignore`**: 忽略 `.gocache/`、`.deploy-cache.json`、`target/`、`*.rs`（Rust 源文件在仓库根目录但被 gitignore，说明 Rust 重写尚未正式纳入版本管理）。
