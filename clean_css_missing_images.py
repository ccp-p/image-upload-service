#!/usr/bin/env python3
"""
清理 CSS 中引用了不存在图片的选择器规则。

用法:
    python clean_css_missing_images.py [--dry-run]

默认直接修改 CSS 文件。加 --dry-run 只打印会删除的规则，不实际修改。
"""

import os
import re
import sys

# ===== 配置 =====
CSS_FILE = r"D:\project\cx_project\china_mobile\gitProject\richinfo_tyjf_xhmqqthy\src\main\webapp\res\wap\css\xdrNormal.css"
# CSS 文件所在目录，用于解析相对路径
CSS_DIR = os.path.dirname(CSS_FILE)
# 备份文件路径
BACKUP_FILE = CSS_FILE + ".bak"


def extract_url_paths(css_text: str) -> list[str]:
    """从 CSS 文本中提取所有 url() 引用的本地路径（排除 http/https/data）"""
    # 匹配 url('...')  url("...")  url(...)
    pattern = r"""url\(\s*['"]?([^'"\)]+?)['"]?\s*\)"""
    urls = []
    for match in re.finditer(pattern, css_text):
        url = match.group(1).strip()
        # 跳过外部 URL 和 data URI
        if url.startswith(("http://", "https://", "data:")):
            continue
        urls.append(url)
    return urls


def resolve_image_path(url_path: str) -> str:
    """将 CSS 中的相对路径解析为文件系统绝对路径"""
    # url 通常是 '../images/xdrNormal/202505/xxx.png'
    # CSS 文件在 css/ 目录，所以 ../ 就是上一级 webapp/res/wap/
    resolved = os.path.normpath(os.path.join(CSS_DIR, url_path))
    return resolved


def is_comment_only(block: str) -> bool:
    """检查一个块是否只包含注释，没有实际的 CSS 属性"""
    # 移除注释后看是否只剩空白
    stripped = re.sub(r'/\*.*?\*/', '', block, flags=re.DOTALL).strip()
    return stripped == ''


def parse_css_blocks(css_text: str) -> list[dict]:
    """
    将 CSS 文本解析为块列表。
    每个块: { 'selector': str, 'body': str, 'full': str, 'start': int, 'end': int, 'type': 'rule'|'comment'|'atrule' }
    """
    blocks = []
    i = 0
    length = len(css_text)

    while i < length:
        # 跳过空白
        if css_text[i].isspace():
            i += 1
            continue

        # 注释
        if css_text[i:i+2] == '/*':
            end = css_text.find('*/', i + 2)
            if end == -1:
                end = length - 2
            blocks.append({
                'type': 'comment',
                'full': css_text[i:end+2],
                'start': i,
                'end': end + 2,
            })
            i = end + 2
            continue

        # @规则（如 @media）- 简单处理，将整个 @media 块作为一个单元
        if css_text[i] == '@':
            # 找到对应的 { }
            brace_start = css_text.find('{', i)
            if brace_start == -1:
                # 没有块，整行作为 at-rule
                line_end = css_text.find('\n', i)
                if line_end == -1:
                    line_end = length
                blocks.append({
                    'type': 'atrule',
                    'selector': css_text[i:line_end].strip(),
                    'body': '',
                    'full': css_text[i:line_end],
                    'start': i,
                    'end': line_end,
                })
                i = line_end
                continue

            # 找到匹配的闭合 }
            depth = 1
            j = brace_start + 1
            while j < length and depth > 0:
                if css_text[j] == '{':
                    depth += 1
                elif css_text[j] == '}':
                    depth -= 1
                j += 1

            block_text = css_text[i:j]
            selector = css_text[i:brace_start].strip()
            body = css_text[brace_start+1:j-1]

            blocks.append({
                'type': 'atrule',
                'selector': selector,
                'body': body,
                'full': block_text,
                'start': i,
                'end': j,
            })
            i = j
            continue

        # 普通 CSS 规则
        brace_start = css_text.find('{', i)
        if brace_start == -1:
            # 剩余文本没有 { 了
            remaining = css_text[i:].strip()
            if remaining:
                blocks.append({
                    'type': 'text',
                    'full': remaining,
                    'start': i,
                    'end': length,
                })
            break

        # 找到匹配的闭合 }
        depth = 1
        j = brace_start + 1
        while j < length and depth > 0:
            if css_text[j] == '{':
                depth += 1
            elif css_text[j] == '}':
                depth -= 1
            j += 1

        selector = css_text[i:brace_start].strip()
        body = css_text[brace_start+1:j-1]
        block_text = css_text[i:j]

        blocks.append({
            'type': 'rule',
            'selector': selector,
            'body': body,
            'full': block_text,
            'start': i,
            'end': j,
        })
        i = j

    return blocks


def process_atrule_block(block: dict, missing_images: list, kept_blocks: list, removed_selectors: list):
    """递归处理 @media 等嵌套块"""
    body = block['body']
    inner_blocks = parse_css_blocks(body)

    kept_inner = []
    for inner in inner_blocks:
        if inner['type'] == 'rule':
            urls = extract_url_paths(inner['body'])
            has_missing = False
            for url in urls:
                img_path = resolve_image_path(url)
                if not os.path.isfile(img_path):
                    has_missing = True
                    missing_images.append((url, img_path, inner['selector']))
            if has_missing:
                removed_selectors.append(inner['selector'].strip())
            else:
                kept_inner.append(inner)
        elif inner['type'] == 'atrule':
            process_atrule_block(inner, missing_images, kept_inner, removed_selectors)
        else:
            kept_inner.append(inner)

    # 重建 @media 块
    if kept_inner:
        new_body = '\n'.join(k['full'] for k in kept_inner)
        new_full = block['selector'] + ' {\n' + new_body + '\n}'
        kept_blocks.append({
            'type': 'atrule',
            'selector': block['selector'],
            'body': new_body,
            'full': new_full,
            'start': block['start'],
            'end': block['end'],
        })
    # 如果所有内部规则都被删除了，整个 @media 块也不保留


def main():
    dry_run = '--dry-run' in sys.argv

    print(f"CSS 文件: {CSS_FILE}")
    print(f"图片目录: {os.path.join(CSS_DIR, '..', 'images', 'xdrNormal')}")
    print(f"模式: {'预览 (dry-run)' if dry_run else '直接修改'}")
    print()

    # 读取 CSS
    with open(CSS_FILE, 'r', encoding='utf-8') as f:
        css_text = f.read()

    original_length = len(css_text)

    # 解析块
    blocks = parse_css_blocks(css_text)

    missing_images = []
    kept_blocks = []
    removed_selectors = []

    for block in blocks:
        if block['type'] == 'comment':
            # 保留注释（但如果是紧跟在被删除规则后面的注释，也一并删除）
            kept_blocks.append(block)
        elif block['type'] == 'text':
            kept_blocks.append(block)
        elif block['type'] == 'atrule':
            process_atrule_block(block, missing_images, kept_blocks, removed_selectors)
        elif block['type'] == 'rule':
            urls = extract_url_paths(block['body'])
            has_missing = False
            for url in urls:
                img_path = resolve_image_path(url)
                if not os.path.isfile(img_path):
                    has_missing = True
                    missing_images.append((url, img_path, block['selector']))
            if has_missing:
                removed_selectors.append(block['selector'].strip())
            else:
                kept_blocks.append(block)

    # 打印结果
    if missing_images:
        print(f"发现 {len(missing_images)} 个缺失图片，涉及 {len(removed_selectors)} 个 CSS 规则:\n")
        for url, img_path, selector in missing_images:
            print(f"  缺失: {os.path.basename(url)}")
            print(f"    路径: {url}")
            print(f"    选择器: {selector[:80]}{'...' if len(selector) > 80 else ''}")
            print()
    else:
        print("没有发现缺失图片的 CSS 规则。")
        return

    if dry_run:
        print(f"[预览模式] 将删除 {len(removed_selectors)} 个规则，不修改文件。")
        return

    # 重建 CSS
    new_css_parts = []
    prev_was_removed = False
    for block in kept_blocks:
        text = block['full']
        # 清理多余空行
        if text.strip():
            new_css_parts.append(text)

    new_css = '\n\n'.join(new_css_parts)

    # 写入新 CSS
    with open(CSS_FILE, 'w', encoding='utf-8') as f:
        f.write(new_css)

    new_length = len(new_css)
    print(f"\n清理完成:")
    print(f"  删除规则数: {len(removed_selectors)}")
    print(f"  原文件大小: {original_length} 字符")
    print(f"  新文件大小: {new_length} 字符")
    print(f"  减少: {original_length - new_length} 字符")


if __name__ == '__main__':
    main()
