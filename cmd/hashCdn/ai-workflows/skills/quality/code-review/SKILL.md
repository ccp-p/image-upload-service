# 技能：代码审查

## 适用场景

提交代码前的最终检查。逐项确认，不过则不提交。

## 检查清单

### 双版本对齐
- [ ] Go 版和 Rust 版的修改是否同步？
- [ ] 正则模式是否两版一致？（对照 dual-version-alignment 速查表）
- [ ] 配置字段是否两版都加了？JSON tag 是否一致？
- [ ] CLI 参数是否两版都处理了？
- [ ] 部署模式常量是否两版同步？

### Hash 处理
- [ ] `renameFileWithHash` 是否保持 copy 语义（不删原文件）？
- [ ] `findAndDeleteOldHashFiles` 是否只删除同 basename 的旧 hash 文件？
- [ ] hash 长度是否使用 config.HashLength 配置值？

### CDN 逻辑
- [ ] CDN 校验前是否先移除了 HTML 注释？
- [ ] `cdnExcludeFiles` 是否用 O(1) map 查找？
- [ ] `cdnPathPrefix` 裁剪逻辑是否正确？

### 部署逻辑
- [ ] `.deploy-cache.json` 是否用 size + modTime 判断文件变化？
- [ ] SVN 操作是否用了 `--keep-local`（delete 时）？
- [ ] 回滚 HTML 是否用 `git checkout HEAD --`？
- [ ] 回滚后 git 流程是否为 add -A -> commit -> pull --rebase -> push？

### 环境处理
- [ ] 新增路径配置是否同时加了 home 和 company 字段？
- [ ] `loadConfig` 中是否正确根据 IS_HOME 选择路径？

### 代码质量
- [ ] 正则是否用了缓存（Go: getRegex + sync.Map）？
- [ ] 是否有硬编码的魔法数字？（应该用常量）
- [ ] 是否有未处理的 error？
- [ ] 日志输出是否包含 emoji 和中文（保持风格一致）？

### 测试
- [ ] `go test -v` 是否通过？
- [ ] `cargo test` 是否通过？
- [ ] 新功能是否有对应的测试用例？
