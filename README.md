# 图片上传服务

一个基于Go和Gin框架的图片上传服务，支持上传图片并返回公网访问地址。

## 功能特性

- 支持多种图片格式：JPG, JPEG, PNG, GIF, WebP
- 文件大小限制：最大10MB
- 自动生成唯一文件名（UUID）
- 按日期分类存储文件
- 提供公网访问URL
- RESTful API接口

## 快速开始

### 1. 安装依赖

```bash
go mod tidy
```

### 2. 运行服务

```bash
go run main.go
```

服务将在 `http://localhost:8080` 启动

### 3. 配置服务器地址

在 `main.go` 文件中修改 `serverHost` 常量为你的实际服务器地址：

```go
const serverHost = "http://your-domain.com"
```

## API接口

### 上传图片

**POST** `/upload`

#### 请求参数

- `image`: 图片文件（form-data格式）

#### 响应示例

成功响应：
```json
{
  "success": true,
  "message": "图片上传成功",
  "url": "http://localhost:8080/uploads/2024/06/05/550e8400-e29b-41d4-a716-446655440000.jpg"
}
```

失败响应：
```json
{
  "success": false,
  "message": "不支持的文件类型，仅支持 jpg, jpeg, png, gif, webp"
}
```

### 健康检查

**GET** `/health`

#### 响应示例

```json
{
  "status": "ok",
  "time": "2024-06-05T10:30:00Z"
}
```

## 使用curl测试

```bash
# 上传图片
curl -X POST -F "image=@your-image.jpg" http://localhost:8080/upload

# 健康检查
curl http://localhost:8080/health
```

## 文件存储结构

```
uploads/
├── 2024/
│   ├── 06/
│   │   ├── 05/
│   │   │   ├── uuid1.jpg
│   │   │   └── uuid2.png
│   │   └── 06/
│   └── 07/
```

## 配置说明

- `uploadDir`: 上传文件存储目录
- `serverHost`: 服务器公网地址
- `maxFileSize`: 最大文件大小限制（默认10MB）

## 注意事项

1. 确保 `uploads` 目录有写入权限
2. 在生产环境中，请修改 `serverHost` 为实际的域名
3. 可以根据需要调整文件大小限制
4. 建议配置反向代理（如Nginx）来处理静态文件服务
