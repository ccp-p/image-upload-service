# cleanUnused

清理未被 CSS 引用的图片，以及引用了缺失图片的 CSS 规则。

## 工作原理

传入一个或多个 CSS 文件的绝对路径，工具会：

1. 解析每个 CSS，提取所有 `url()` 中指向本地文件的相对路径；
2. 以 CSS 文件所在目录为基准，解析出图片的绝对路径；
3. **过时 CSS**：引用了不存在图片的规则（断链规则）；
4. **过时图片**：扫描图片所在目录，找出未被任何传入 CSS 引用的图片（孤儿图片）。

默认只报告（dry-run），不修改任何文件。

## 编译

```bat
cd cmd\cleanUnused
go build -o cleanUnused.exe .
```

## 用法

```
cleanUnused [选项] <css文件> [<css文件>...]
``+
### 选项

| 选项 | 说明 |
| --- | --- |
| `--img-dir <dir>` | 额外的图片扫描目录，可多次指定；未指定时自动取 CSS 引用图片的父目录 |
| `--delete` | 删除未被引用的图片 |
| `--move <dir>` | 将未被引用的图片移动到指定目录（与 `--delete` 互斥，更可恢复） |
| `--clean-css` | 重写 CSS，移除引用了缺失图片的规则（生成 `.bak` 备份） |
| `--yes` | 跳过删除/重写前的确认 |
| `--ext <list>` | 图片扩展名白名单，逗号分隔，默认 `png,jpg,jpeg,gif,webp,svg,bmp,ico` |

### 示例

```bat
:: 预览：只报告，不动文件
cleanUnused D:\proj\res\wap\css\xdrNormal.css

:: 重写 CSS 删除断链规则（备份 .bak）
cleanUnused --clean-css --yes D:\proj\res\wap\css\xdrNormal.css

:: 把孤儿图片移到 _unused 目录（删除用 --delete）
cleanUnused --move .\_unused --yes D:\proj\res\wap\css\xdrNormal.css
```

## 注意事项

- **hash 版本图片始终排除**：形如 `foo.88ade0f6.png` 的 hash 版本图片是 hashCdn 的构建产物，hashCdn 重建时会自动清理过期 hash 文件，因此本工具不会将其当作孤儿图片删除（报告里会单独统计「已保护 hash 版本图片」数量）。删除孤儿的原版图片后，下次运行 hashCdn 自然不会再生成对应 hash 文件。
- 仅按 CSS 中的 `url()` 判断引用；通过 HTML/JS 内联引用或 `<img>` 标签使用的图片不在此工具范围内，请确认后再删除。
- 若多个 CSS 共用同一个图片目录，请**一并传入**，否则会被误判为孤儿图片。
- 默认 dry-run；破坏性操作前会要求确认，`--yes` 可跳过。
- `--clean-css` 会重建 `@media` 等嵌套块的格式（内容不变，但换行/缩进可能改变），已生成 `.bak` 备份。
