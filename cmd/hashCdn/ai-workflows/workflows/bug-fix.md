# 工作流：Bug 修复

## 适用场景

修复 hash 处理、CDN 替换、部署复制等环节的已知 Bug。

## 流程

### 1. 定位
- 确认 Bug 出现在 Go 版、Rust 版、还是两版都有
- 如果只有一版有 Bug，另一版是否也有同样问题？对照 `dual-version-alignment/SKILL.md`

### 2. 修复
- 先在出 Bug 的版本修复
- 立即检查另一版是否有相同问题
- 如果另一版没有 Bug，说明两版已经不一致了，记录差异原因

### 3. 测试
- 跑 `go test -v` 或 `cargo test`
- 用 `-dry-run` 复现场景验证修复
- 如果涉及部署，用 mode 4（不替换 CDN + copy）验证

### 4. 回归
- 确认修复没有破坏其他功能
- 重点关注正则修改是否影响 CSS url() 解析、HTML 引用收集
