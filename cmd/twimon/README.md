# Twitter/X 用户更新监控服务

监控指定 Twitter/X 用户的更新，检测到变化时通过 PushPlus 发送通知。

## 监控内容

- 推文数量变化（新增/删除推文）
- 昵称变更
- 简介变更
- 头像更新
- 横幅更新

## 数据源

使用 [fxtwitter](https://github.com/FixTweet/FxTwitter) API 获取用户信息，无需 Twitter API Key，免费且稳定。

## 使用方法

### 环境变量

| 变量名 | 说明 | 必填 |
|--------|------|------|
| PUSH_PLUS | PushPlus token | 是 |
| HTTP_PROXY | HTTP 代理地址 | 否（默认 http://127.0.0.1:7890）|

### 命令行参数

```
twimon.exe [参数]

  -user string      监控的用户名，不带 @（默认 "1Ylik"）
  -interval int     检查间隔，分钟（默认 10）
  -proxy string     HTTP 代理地址（留空则用环境变量或默认值）
                    （none=直连，GitHub Actions 等海外环境使用）
  -state string     状态文件路径（默认同目录 state.json）
  -once             只检查一次，不进入循环
  -test             发送测试通知并退出
```

### 快速启动

双击 `start.bat` 打开菜单界面，选择对应操作即可。

### 命令行示例

```bash
# 启动监控（默认 10 分钟检查一次）
twimon.exe

# 自定义用户和间隔
twimon.exe -user=elonmusk -interval=5

# 只检查一次
twimon.exe -once

# 发送测试通知
twimon.exe -test

# 自定义代理
twimon.exe -proxy=http://127.0.0.1:7890
```

## GitHub Actions 部署

`.github/workflows/twimon.yml` 已配置每天北京时间 09:00（UTC 01:00）自动检查一次。

1. 仓库 Settings -> Secrets and variables -> Actions，添加 `PUSH_PLUS` secret
2. 工作流运行后会把 `cmd/twimon/state-gh.json` 提交回仓库，作为下次对比基线
3. 手动触发：Actions -> twimon -> Run workflow

说明：

- Actions 服务器在海外，直连 fxtwitter 和 PushPlus，不需要代理（`-proxy=none`）
- 定时任务可能有几分钟到几十分钟的延迟，偶发跳过，第二天会补上
- GitHub 定时任务按 UTC 调度，改时间改 `cron` 表达式即可
- 无变化时不产生提交，只有状态变化时才有一次 state 提交
- 本地运行用 `state.json`，Actions 用 `state-gh.json`，两边状态独立互不干扰

## 工作原理

1. 通过 fxtwitter API 获取用户信息（推文数、粉丝数、昵称、简介等）
2. 与本地 state.json 中保存的上次状态对比
3. 检测到变化时，通过 PushPlus 发送 Markdown 格式通知
4. 更新本地状态文件
5. 等待指定间隔后重复

## 文件说明

| 文件 | 说明 |
|------|------|
| main.go | 主程序源码 |
| twimon.exe | 编译后的可执行文件 |
| start.bat | 启动菜单脚本 |
| state.json | 状态文件（自动生成）|
