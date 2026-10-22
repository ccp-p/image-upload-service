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
| 16 | 替换CDN + 跳过hash（原始文件名直走CDN） | copy | 沿用配置 |
| 17 | 替换CDN + 跳过hash（原始文件名直走CDN） | copy-commit | 沿用配置 |

## bat 脚本扩展模式

| mode | 行为 |
|------|------|
| 10 | 仅部署 copy（不处理 hash） |
| 11 | 仅部署 copy-commit（不处理 hash） |
| 12 | 回退 dest 仓库本地变更（自动识别 Git/SVN） |
| 15 | 仅清理 dest 旧 hash 文件（不部署；保护现网/HTML历史/近期部署，两版 `-clean-hash`） |

## 部署流程

1. hash 版本化 -> 2. CDN 替换（可选） -> 3. 复制到 dest -> 4. CDN 校验 -> 5. 提交（可选，按仓库类型） -> 6. 回滚 HTML + git push（可选）

## dest 仓库自动识别

部署目标运行时探测（优先 Git，其次 SVN），两版行为一致：

- **Git**：部署前 `git pull origin master`；提交时 `git add -A` → `git commit` → `git push origin master`（push 失败自动走本机 7890 代理重试）
- **SVN**：部署前 `svn update`；提交时 `svn add` + `svn commit`（原有流程保留）
- **回退（mode 12）**：Git 用 `reset --hard` + `clean -fd`，SVN 用递归 revert + 清理未版本化文件
- **历史保护查询**：Git 用 `git log` / `git show` / `git log --format=%ct`，SVN 用 `svn log --xml` / `svn cat` / `svn info --xml`；任何查询失败跳过清理（宁可多留不可误删）

当前 `version.config.json` 指向 Git 仓库：`D:\project\cx_project\china_mobile\gitHuidu\xhmqqthy-res\res\wap`（公司）/ `D:\job_project\china_mobile\gitHuidu\xhmqqthy-res\res\wap`（家里）。

## 关键实现

- **copy**: 只复制文件到 dest，不提交 SVN
- **copy-commit**: 复制后自动提交 dest 仓库（Git: add/commit/push；SVN: add/commit），提交信息取自源仓库 Git 最新 commit 或自定义
- **回滚**: git checkout HEAD -- 恢复 HTML，然后 git add -A -> commit -> pull --rebase -> push
- **盲盒组件**: cdnExcludeFiles 列表中的文件不做 CDN 替换，保持相对路径

## 常见问题

- **mode 1-3**: 全量 CDN 替换，适合所有资源都走 CDN 的场景
- **mode 4-6**: 不替换 CDN，适合调试或不需要 CDN 的环境
- **mode 7-9**: 排除盲盒组件后替换，适合部分组件不走 CDN 的场景
- **mode 10-11**: 跳过 hash 处理，只做部署复制
- **mode 16-17**: HTML 引用替换为 CDN 域名 + 原始文件名（复用剩余资源 CDN 替换阶段），不生成/清理 hash 文件，部署只复制原始文件（`copy_base_file_only`）；回滚设置沿用配置文件

## 修改部署逻辑时的检查

- [ ] 修改后 mode 1-9 的行为是否两版一致？
- [ ] 新增模式是否同时更新了 Go 的常量定义和 Rust 的对应逻辑？
- [ ] bat 脚本的模式列表是否同步更新？
- [ ] validateCDNResources 是否在复制后正确触发？
