# hashCdn AI 工作流体系

让 AI 在修改 hashCdn 代码时"不用教第二遍"的约定体系。

## 这是什么

一套技能 + Hook + 工作流的组合，让 AI 每次启动时自动加载项目约定，减少因信息不一致导致的返工。

## 目录结构

```
ai-workflows/
├── AGENTS.md                              AI 协作入口（必读）
├── README.md                              使用指南（本文件）
├── workflows/
│   ├── feature-development.md             功能开发工作流
│   ├── bug-fix.md                         Bug 修复工作流
│   ├── hotfix.md                           紧急修复工作流
│   └── refactor.md                        代码重构工作流
├── skills/
│   ├── project/
│   │   └── dual-version-alignment/        Go/Rust 双版本对齐（核心技能）
│   │       └── SKILL.md
│   ├── business/
│   │   └── deploy-modes/                  部署模式 9 种组合
│   │       └── SKILL.md
│   └── quality/
│       └── code-review/                   代码审查检查清单
│           └── SKILL.md
├── hooks/
│   └── config.yaml                        Hook 配置
└── templates/                             代码模板（待填充）
```

## 核心技能

### dual-version-alignment（最重要的技能）

hashCdn 维护 Go + Rust 双版本对等实现。修改任何一版时，必须检查另一版是否同步。这个技能包含：
- Go/Rust 函数对应关系速查表
- 正则模式对照表
- 配置字段对齐规则
- CLI 参数对齐规则
- 修改后的检查清单

### deploy-modes

9 种部署模式的组合规则（CDN 行为 x 提交行为 x 回滚行为），以及修改部署逻辑时的检查项。

### code-review

提交前的最终检查清单，涵盖双版本对齐、hash 处理语义、CDN 逻辑、部署逻辑、环境处理、代码质量。

## Hook 保护

`hooks/config.yaml` 定义了以下自动检查：

- **修改代码前**：提醒加载双版本对齐技能、保留原文件、双环境路径
- **修改代码后**：检查另一版是否同步、配置字段是否一致
- **编辑文件后**：自动跑 go test / cargo test、检查正则对齐
- **技能加载后**：记录使用情况

## 怎么用

1. **修改代码前**：让 AI 读 `AGENTS.md`，它会知道体系的存在
2. **涉及双版本修改**：AI 自动加载 `dual-version-alignment/SKILL.md`
3. **提交前**：AI 自动加载 `code-review/SKILL.md`，逐项检查
4. **部署相关问题**：AI 自动加载 `deploy-modes/SKILL.md`

## 后续扩展

随着使用积累，可以继续添加：
- `skills/project/hash-semantics/` — hash 处理的详细语义规范
- `skills/business/cdn-logic/` — CDN 替换和校验的详细规则
- `skills/workflow/cross-module/` — 跨模块协作的约定
- `templates/` — Go/Rust 代码模板
