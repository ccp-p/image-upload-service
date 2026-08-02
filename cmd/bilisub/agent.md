# bilisub Agent Guide

Bilibili 字幕下载与 AI 总结工具。两种工作模式：CLI 命令行直接拉取字幕，或 HTTP 服务器配合浏览器油猴脚本批量获取多 P 字幕并自动保存到磁盘。服务器内置 AI 总结代理，可将字幕文本转发至 OpenAI 兼容的 LLM API 生成摘要。

Go 后端单 module，无第三方依赖（仅标准库）。浏览器侧通过 `bilisub_subtitle.user.js` 油猴脚本注入浮动面板。

## 项目布局

```
image-upload-service/                 Go module: image-upload-service
├── go.mod / go.sum
└── cmd/bilisub/                      <- 当前工作目录
    ├── main.go                       Go 入口：CLI 逻辑 + Bilibili API 类型 + 格式转换
    ├── server.go                     HTTP 服务器：字幕保存 + AI 总结代理 + 配置存取
    ├── bilisub_subtitle.user.js      油猴脚本：浏览器侧浮动面板（shadow DOM）
    ├── main.js                       第三方参考脚本（bilibili 视频下载 by injahow，非本项目代码）
    ├── start_server.bat              Windows 启动脚本（杀旧端口 + 启动 -serve）
    ├── tempCodeRunnerFile.bat        VS Code Code Runner 临时文件（内容同 start_server.bat）
    ├── bilisub.exe                   编译后的二进制
    ├── bilisub.exe~                  旧版本备份
    └── subtitles/                    默认输出目录（gitignore）
```

## 核心概念

**双模式架构**：CLI 模式通过 Bilibili 公开 API 直接下载字幕，适合单次使用；Server 模式启动 HTTP 服务，由油猴脚本在浏览器侧采集字幕后批量回传，适合多 P 视频批量处理。

**Bilibili API 三步链路**：`view` 接口获取视频元信息（aid + 多 P 的 cid 列表）→ `player/wbi/v2` 接口获取每 P 的字幕列表 → `subtitle_url` 下载字幕 JSON。CLI 和油猴脚本走相同的 API 链路，区别在于 CLI 用 Go 的 `http.Client`，油猴脚本用浏览器 `fetch`（带 cookie 凭证）。

**SESSDATA**：Bilibili 登录态 cookie。部分字幕（尤其是会员专享或高清）需要登录才能获取。CLI 通过 `-sessdata` 参数传入，油猴脚本通过 `fetch` 的 `credentials: 'include'` 自动携带浏览器 cookie。

**字幕格式**：Bilibili 原始字幕为 JSON（`{from, to, content}` 数组）。工具支持三种输出格式：VTT（WebVTT，带 `WEBVTT` 头）、SRT（SubRip，带序号）、TXT（纯文本，去重去时间戳）。油猴脚本额外支持 TXT 格式，CLI 仅支持 VTT/SRT。

**油猴脚本 + 服务器协作**：脚本检测 `localhost:9876/health` 判断服务器是否运行。运行时可将字幕批量 POST 到 `/api/subtitles` 自动保存到磁盘子目录；未运行时退化为浏览器侧下载 zip。

**AI 总结代理**：服务器 `/api/summarize` 端点接收字幕文本 + LLM 配置，转发到 OpenAI 兼容 API（`/v1/chat/completions`，SSE 流式）。解析 SSE 流中的 `choices[0].delta.content` 累积摘要，跳过 `reasoning_content`（思考链）。API Key 等配置可持久化到 `.ai_config.json`。默认提示词要求 LLM 以中文回复，只返回 HTML body 内容（不含 `<!DOCTYPE>`/`<html>`/`<head>`/`<style>`），使用预定义组件 class 名。渲染器自动注入深色主题 CSS（仿课堂架构图解风格：深色背景 `#0a0e17` + 金色强调 `#f0c040` + 卡片式布局），通过 `iframe.srcdoc` 展示，右上角一键复制图标用 `ClipboardItem` 写入 `text/html`，可直接粘贴到 Word/Notion 等文档。

## 工作流程

### CLI 模式

1. **解析 BV id**：`extractBVID` 接受纯 BV id、完整 bilibili URL 或 b23.tv 短链，用正则 `BV[0-9A-Za-z]{10}` 提取。
2. **获取视频信息**：调用 `view` 接口，拿到 aid、标题、所有 P 的 cid 列表。
3. **筛选页面**：`-p 0`（默认）处理全部 P，`-p N` 只处理第 N 页。
4. **逐页获取字幕**：对每个 P 调用 `player/wbi/v2` 拿字幕列表。`-lang` 指定语言偏好（匹配 `lan_doc` 或 `subtitle_url`），默认取第一个。
5. **下载字幕内容**：从 `subtitle_url` 拉取 JSON，解析 `body` 数组。
6. **格式转换并保存**：转 VTT 或 SRT，文件名 `标题_PN_分P名.ext`（单 P 时省略分P后缀），写入 `-o` 指定目录。

### Server 模式

1. **启动服务器**：`-serve -port 9876 -o subtitles`，监听 HTTP。
2. **油猴脚本注入**：用户打开 bilibili 视频页，脚本从 `window.__INITIAL_STATE__` 读取视频信息，渲染浮动面板。
3. **用户操作**：在面板选择格式（TXT/VTT/SRT）、勾选要获取的 P、可选勾"保存到服务器"。
4. **批量获取**：脚本逐 P 调用 Bilibili API 拉字幕，带 800ms 延迟避免触发风控。无字幕时尝试点击 CC 按钮开启字幕再重试。
5. **回传服务器**：若勾选"保存到服务器"，批量 POST 到 `/api/subtitles`，服务器按 `标题/分P名.ext` 结构保存到输出目录。
6. **AI 总结**：用户点"AI总结"按钮，脚本合并所有字幕纯文本，连同 LLM 配置 POST 到 `/api/summarize`，服务器转发到 LLM API 并流式返回 HTML 摘要。脚本用 iframe 渲染 HTML，自动复制到剪贴板（`text/html` 格式），右上角一键复制图标可随时重新复制。

## CLI 参数

```
bvid / 位置参数        Bilibili 视频 BV id 或完整 URL
-sessdata <cookie>    SESSDATA cookie 值（登录态，获取会员字幕）
-p <page>             指定页码（0 = 全部页，默认 0）
-format <vtt|srt>      输出格式（默认 vtt）
-list                 仅列出可用字幕，不下载
-o <dir>              输出目录（默认当前目录）
-lang <code>          字幕语言偏好（如 zh-CN, ai-zh；默认第一个）
-serve                启动 HTTP 服务器模式
-port <port>          HTTP 服务器端口（默认 9876，serve 模式）
```

## HTTP API（Server 模式）

| 路由 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 健康检查，返回 `{"status":"ok"}`，油猴脚本据此判断服务器状态 |
| `/api/subtitles` | POST | 接收字幕批次 JSON（`subtitleBatch`），按 `标题/分P名.ext` 保存到输出目录 |
| `/api/summarize` | POST | AI 总结代理。接收字幕文本 + LLM 配置，转发到 OpenAI 兼容 API，流式返回摘要 |
| `/api/config` | GET/POST | AI 配置存取。GET 返回脱敏后的配置（API Key 仅显示首尾 4 位），POST 保存到 `.ai_config.json` |
| `/` | GET | 简单 HTML 状态页 |

所有端点启用 CORS（`Access-Control-Allow-Origin: *`），支持 OPTIONS 预检。

`/api/summarize` 请求体：
```json
{
  "text": "字幕纯文本",
  "api_url": "https://your-llm-api.com/",
  "api_key": "sk-...",
  "model": "glm-5.2",
  "system_prompt": "你是一位专业的视频内容分析师兼前端设计师。请分析视频字幕，生成一份深色主题的可视化要点解析页面..."
}
```

服务器自动处理 `api_url`：若以 `/chat/completions` 结尾则原样使用，以 `/v1` 结尾则补全路径，否则追加 `/v1/chat/completions`。字幕文本超过 100000 字符时截断。LLM 请求超时 300 秒。

## 代码结构

### main.go（CLI 入口 + API 类型 + 格式转换）

- **API 响应类型**：`ViewInfo`（view 接口）、`SubtitleList`（player/wbi/v2 接口）、`SubtitleBody`（字幕 JSON）。
- **HTTP 辅助**：`httpClient`（30s 超时）、`fetchJSON`（带 UA + Referer + SESSDATA cookie）。
- **核心 API 调用**：`getViewInfo`、`getSubtitleList`、`getSubtitleContent`。
- **格式转换**：`toVTT`（带 `WEBVTT` 头）、`toSRT`（带序号）、`formatVTTTime`（秒转 `HH:MM:SS.mmm`）。
- **文件名辅助**：`sanitizeFilename`（去除 `\/\\:*?"<>|`）、`buildFilename`（多 P 时追加 `_PN_分P名`）。
- **BV id 提取**：`extractBVID` 支持 URL、b23.tv、纯 BV id，用正则兜底。
- **`main`**：flag 解析 → serve 模式分支 → CLI 主流程。

### server.go（HTTP 服务器）

- **`runServer`**：创建 `ServeMux`，注册路由，监听端口。
- **`/api/subtitles`**：解析 `subtitleBatch`，按视频标题建子目录，逐条保存。文件名取 `sub.Part`（分P名），多 P 和单 P 逻辑一致。
- **`/api/summarize`**：构建 OpenAI 兼容请求（`stream: true` + `chat_template_kwargs.thinking: true`），用 `bufio.Scanner` 解析 SSE 流，累积 `delta.content`，跳过 `reasoning_content`。空结果时回退解析非流式响应。
- **`/api/config`**：读写 `.ai_config.json`（`0600` 权限）。GET 时对 API Key 脱敏（首 4 + `...` + 尾 4）。
- **辅助**：`setCORS`（统一 CORS 头）、`writeJSON`（JSON 响应）。
- **数据类型**：`subtitleItem`（单条字幕）、`subtitleBatch`（批次，含标题/BVID/格式/字幕数组）。

### bilisub_subtitle.user.js（油猴脚本）

- **视频信息获取**：优先从 `window.__INITIAL_STATE__`（`videoData` 或 `videoInfo`）读取，回退到 URL 正则 + API 调用。
- **浮动面板**：shadow DOM 注入，Catppuccin Mocha 配色。含格式选择、服务器保存开关、AI 配置（URL/Key/Model/Prompt）、P 列表多选、获取/复制/总结按钮。
- **字幕获取**：`getSubtitleList` + `getSubtitleContent`，逐 P 串行（`DELAY_MS = 800`）。无字幕时 `tryClickCC` 点击 CC 按钮后重试。
- **语言偏好**：优先匹配 `lan` 以 `zh` 开头的字幕。
- **本地存储**：AI 配置、自动获取/总结偏好存 `localStorage`。
- **服务器协作**：`checkServer` 探测健康，`sendToServer` 批量回传，`summarizeSubtitles` 调 AI 总结。
- **结果渲染**：`renderSummary` 始终用 `wrapHTMLDoc` 包裹内容，统一注入深色主题 CSS（`THEME_CSS` 常量，仿课堂架构图解风格）。如果 LLM 返回完整 HTML 文档，会先提取 `<body>` 内容再重新包裹；如果返回 Markdown，先用 `markdownToHTML` 转换（inline 样式已适配深色主题）再包裹。最终通过 `iframe.srcdoc` 渲染（样式隔离，深色背景）。
- **深色主题设计系统**（`THEME_CSS`）：CSS 变量定义颜色系统（`--bg`/`--surface`/`--accent`/`--blue`/`--green` 等），组件包括 `.hero`、`.section-label`、`.concept-card[data-color]`、`.code-block`（红绿灯头）、`.flow-step[data-color]`、`.warn-box`、`.tip-box`、`.cmp-table`、`.summary-bar`。字体引入 Google Fonts：Noto Serif SC（标题）、JetBrains Mono（标签/代码）、Noto Sans SC（正文）。
- **一键复制**：摘要区右上角剪贴板图标按钮，点击复制渲染后的 HTML 到剪贴板，复制后图标变绿色对勾 1.5 秒后恢复。生成摘要时也会自动复制一次。
- **纯文本去重**：`toPlainText` 去时间戳、相邻重复行去重。
- **URL 监听**：`watchUrlChange` 监听 SPA 路由变化，切换视频时重建面板。

### main.js

第三方油猴脚本 `bilibili视频下载`（作者 injahow，v2.9.1），用于下载视频本体。非本项目代码，仅作参考。支持 Web/RPC/Blob/Aria 下载方式、flv/dash/mp4 格式、港澳台番剧、字幕弹幕、换源播放。

## 构建与运行

```powershell
cd D:\self_project\go_project\image-upload-service
go build -o cmd\bilisub\bilisub.exe ./cmd/bilisub/
```

CLI 模式：
```powershell
# 下载全部 P 的 VTT 字幕
cmd\bilisub\bilisub.exe -bvid BV1xx411x7xx -format vtt -o subtitles

# 指定第 2 P，SRT 格式，带登录态
cmd\bilisub\bilisub.exe -sessdata "your_sessdata" -p 2 -format srt BV1xx411x7xx

# 仅列出可用字幕
cmd\bilisub\bilisub.exe -list BV1xx411x7xx

# 直接传完整 URL
cmd\bilisub\bilisub.exe "https://www.bilibili.com/video/BV1xx411x7xx"
```

Server 模式：
```powershell
# 直接运行
cmd\bilisub\bilisub.exe -serve -o subtitles

# 或用 bat 脚本（自动杀旧端口 + 启动）
cmd\bilisub\start_server.bat
```

服务器启动后：
1. 在浏览器安装 `bilisub_subtitle.user.js` 油猴脚本（Tampermonkey / Violentmonkey）。
2. 打开任意 bilibili 视频页面，右下角出现浮动面板。
3. 勾选要获取的 P，选择格式，点"获取选中P字幕"。
4. 勾选"保存到服务器"可自动保存到磁盘；否则下载 zip。
5. 在 AI Config 中填写 LLM API 信息后，点"AI总结"生成摘要。

## 开发约定

- **纯标准库**：Go 代码不引入第三方依赖，仅用标准库（`net/http`、`encoding/json`、`flag`、`regexp` 等）。新增功能应保持零依赖。
- **单文件拆分**：`main.go` 承载 CLI 逻辑和 API 类型，`server.go` 承载 HTTP 服务器。两者同属 `package main`，共享类型定义。
- **油猴脚本独立**：`bilisub_subtitle.user.js` 是纯浏览器 JS，不参与 Go 编译。修改面板逻辑、API 交互、UI 样式都在此文件内。
- **编码**：bat 脚本用 `chcp 65001`（UTF-8），Go 代码和油猴脚本使用 UTF-8。中文日志和 emoji 可直接出现在代码中。
- **文件名净化**：`sanitizeFilename` 统一去除 `\/\\:*?"<>|`，CLI 和服务器共用。输出目录按视频标题建子目录，避免不同视频字幕混杂。
- **CORS 全开**：服务器对所有路由设置 `Access-Control-Allow-Origin: *`，因为油猴脚本的页面域是 bilibili.com，服务器是 localhost，跨域必须放行。
- **AI 配置安全**：`.ai_config.json` 以 `0600` 权限存储，GET 接口返回时对 API Key 脱敏。油猴脚本侧也将配置存入 `localStorage`，避免每次重填。
- **SSE 流式解析**：`/api/summarize` 逐行扫描 `data: ` 前缀，累积 `delta.content`，遇到 `[DONE]` 终止。`reasoning_content`（思考链）被跳过不进入摘要。空结果时回退到非流式 JSON 解析。
- **默认提示词**：要求 LLM 以中文回复，只返回 HTML body 内容（不含 `<!DOCTYPE>`/`<html>`/`<head>`/`<style>`），使用预定义组件 class 名（`.concept-card`、`.code-block`、`.flow-step` 等），CSS 由渲染器自动注入。提示词中列出所有可用组件及其 class 结构。油猴脚本侧 `DEFAULT_PROMPT` 变量可自定义，配置存入 `localStorage`。
- **API Key 传递**：油猴脚本将 API Key 明文 POST 到服务器，服务器再转发到 LLM。这是本地工具的简化设计，不在公网暴露。
- **`.gitignore`**：忽略 `bilisub.exe`、`tempCodeRunnerFile.bat`、`cmd/bilisub/subtitles/`、`subtitles/`。`.ai_config.json` 未显式忽略但位于 gitignore 的 subtitles 目录内，实际不会入库。
- **输出目录结构**：`subtitles/<视频标题>/<分P名>.<ext>`。多 P 视频每 P 一个文件，单 P 视频直接以分P名（或标题）命名。
