# AGENTS.md — hashCdn AI 工作流体系

本文件是 AI 协作的入口。hashCdn 项目维护 Go + Rust 双版本对等的静态资源 hash 化工具，以下是 AI 必须遵守的约定。

## 项目一句话

为 H5 页面（xdrNormal.html 及其组件）生成带 MD5 hash 的静态资源副本，替换 CDN 引用，复制到 SVN 灰度目录并提交。Go 版和 Rust 版功能等价，共享 `version.config.json`。

## 体系结构

```
ai-workflows/
├── AGENTS.md                    ← 你正在读的文件
├── workflows/                   工作流定义（功能开发/Bug修复/紧急修复/重构）
├── skills/                      技能库
│   ├── project/                 项目级技能（双版本对齐等）
│   ├── business/                业务级技能（部署模式、CDN 逻辑等）
│   ├── workflow/                工作流技能
│   └── quality/                 质量保障技能（代码审查）
├── hooks/                       钩子配置
└── templates/                   代码模板
```

## 必读技能

每次修改代码前，按需加载：

1. **dual-version-alignment**（`skills/project/dual-version-alignment/SKILL.md`）
   修改任何一版代码时，必须检查另一版是否同步。这是本项目返工率最高的环节。

2. **code-review**（`skills/quality/code-review/SKILL.md`）
   提交前的检查清单。

## 关键约定（AI 必须遵守）

- **保留原文件**：hash 处理永远生成副本（`renameFileWithHash` 是 copy 语义），不删除原始无 hash 文件。
- **清理旧 hash**：新 hash 文件生成后，自动删除同 basename 的旧 hash 文件。
- **双环境**：通过 `IS_HOME` 环境变量区分家里/公司，所有路径配置都要成对（home/company）。
- **正则缓存**：Go 用 `getRegex` + `sync.Map`；Rust 在 `patterns.rs` 中手写匹配函数。
- **CDN 校验**：校验前先移除 HTML 注释 `<!-- -->`，避免注释中的 URL 导致误报。
- **部署模式 1-9**：三维度组合（CDN 行为 x 提交行为 x 回滚行为），修改时两版必须同步。
- **功能对齐**：改 Go 版必须同步改 Rust 版，反之亦然。共享 `version.config.json`，配置字段必须一致。

## 代码位置

- Go 版：`cmd/hashCdn/main.go`（单文件，~2400 行）
- Rust 版：`config.rs` / `deploy.rs` / `json.rs` / `md5.rs` / `patterns.rs` / `version_manager.rs` / `main_new.rs`
- 配置：`cmd/hashCdn/version.config.json`（双版本共用）
- 测试：`cmd/hashCdn/main_test.go`（Go）+ `patterns.rs` 内 `#[cfg(test)]`（Rust）
- 运行脚本：`run_hash_cdn.bat`（Go）/ `run_hash_cdn_rs.bat`（Rust）

## 构建

```powershell
# Go
cd D:\project\my_go_project\image-upload-service\cmd\hashCdn
go build -o hashCdn.exe main.go

# Rust
cd D:\project\my_go_project\image-upload-service
cargo build --release
```
