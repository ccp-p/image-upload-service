# HTML Hash CDN 工具

自动为 HTML、CSS、JS 和图片文件生成带 hash 的版本，并自动更新引用。

## 快速开始

### 1. 配置文件

编辑 `version.config.json`：

```json
{
  "rootDir": ".",
  "cdnDomain": "",
  "hashLength": 8,
  "singleHTMLFile": "D:\\path\\to\\your\\index.html",
  "htmlFiles": [],
  "excludeDirs": ["node_modules", ".git", "dist", "build"]
}
```

**配置项说明：**
- `rootDir`: 项目根目录
- `cdnDomain`: CDN 域名（可选，留空则使用相对路径）
- `hashLength`: hash 长度（默认 8）
- `singleHTMLFile`: 要处理的单个 HTML 文件路径
- `htmlFiles`: 要批量处理的 HTML 文件列表
- `excludeDirs`: 扫描时排除的目录

### 2. 运行方式

#### 方式 1: 使用 bat 文件（推荐）

直接双击 `run.bat` 文件。

#### 方式 2: 命令行

```bash
# 使用配置文件中的设置
go run main.go -config=version.config.json

# 命令行指定文件（优先级高于配置文件）
go run main.go -file="D:\path\to\index.html"

# 扫描所有 HTML 文件
go run main.go -all

# 指定 CDN 域名
go run main.go -cdn="https://cdn.example.com"
```

### 3. 高级用法

#### 使用 CDN 域名

双击 `run_with_cdn.bat`，或修改该文件中的 `CDN_DOMAIN` 变量。

#### 批量处理多个文件

在 `version.config.json` 中设置 `htmlFiles` 数组：

```json
{
  "htmlFiles": [
    "page1.html",
    "page2.html",
    "page3.html"
  ]
}
```

## 功能特性

- ✅ 自动生成带 hash 的文件副本
- ✅ 自动删除旧的 hash 文件
- ✅ 保留原始文件
- ✅ 自动更新 HTML 中的资源引用
- ✅ 处理 CSS 中的图片引用
- ✅ 支持 CDN 域名
- ✅ 生成版本映射文件

## 输出

处理完成后会生成：
- 带 hash 的文件（如 `style.abc12345.css`）
- `.version-map.json` 版本映射文件

## 注意事项

1. 程序会保留原始文件（无 hash）
2. 旧的 hash 文件会被自动删除
3. 建议在处理前备份重要文件
4. 确保配置文件中的路径使用双反斜杠 `\\`
