# 工作流：紧急修复

## 适用场景

线上问题需要快速修复并部署。

## 流程

### 1. 快速定位
- 确认是哪个环节：hash 生成？CDN 替换？文件复制？SVN 提交？
- 检查 version.config.json 配置是否正确
- 用 `-dry-run` 快速预览当前配置

### 2. 最小修改
- 只改必须改的代码
- 如果只改 Go 版，在修复后标注 Rust 版需要同步（但不在本次 hotfix 中做）
- 修改 commit message 记录 hotfix 原因

### 3. 验证
- `go test -v` 确保不破坏现有测试
- 用 mode 6（保持相对路径 + copy-commit + 回滚 + git push）部署
- 部署后确认 CDN 校验通过

### 4. 事后同步
- Hotfix 完成后，安排时间同步 Rust 版
- 记录到 "如果 AI 知道就好了" 列表
