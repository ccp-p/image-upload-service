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