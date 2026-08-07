# 技能：部署模式

## 适用场景

修改部署逻辑、新增部署模式、调试部署问题时参考。

## 9 种部署模式

三维度组合：CDN 行为 x 提交行为 x 回滚行为。

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

## bat 脚本扩展模式

| mode | 行为 |
|------|------|
| 10 | 仅部署 copy（不处理 hash） |
| 11 | 仅部署 copy-commit（不处理 hash） |
| 12 | 回退 dest SVN（svn revert） |

## 部署流程

1. hash 版本化 -> 2. CDN 替换（可选） -> 3. 复制到 dest -> 4. CDN 校验 -> 5. SVN 提交（可选） -> 6. 回滚 HTML + git push（可选）

## 关键实现

- **copy**: 只复制文件到 dest，不提交 SVN
- **copy-commit**: 复制后自动 svn commit，提交信息取自 Git 最新 commit 或自定义
- **回滚**: git checkout HEAD -- 恢复 HTML，然后 git add -A -> commit -> pull --rebase -> push
- **盲盒组件**: cdnExcludeFiles 列表中的文件不做 CDN 替换，保持相对路径

## 常见问题

- **mode 1-3**: 全量 CDN 替换，适合所有资源都走 CDN 的场景
- **mode 4-6**: 不替换 CDN，适合调试或不需要 CDN 的环境
- **mode 7-9**: 排除盲盒组件后替换，适合部分组件不走 CDN 的场景
- **mode 10-11**: 跳过 hash 处理，只做部署复制

## 修改部署逻辑时的检查

- [ ] 修改后 mode 1-9 的行为是否两版一致？
- [ ] 新增模式是否同时更新了 Go 的常量定义和 Rust 的对应逻辑？
- [ ] bat 脚本的模式列表是否同步更新？
- [ ] validateCDNResources 是否在复制后正确触发？
