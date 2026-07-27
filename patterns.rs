//! Pattern matching functions replacing Go's regex usage.
//! All patterns are implemented with pure string operations.

use std::collections::HashMap;

const HASH_EXTS: &[&str] = &[
    "css", "js", "jpg", "jpeg", "png", "gif", "svg", "webp", "ico",
];

/// Checks if a string is a valid lowercase hex hash (4-64 chars, [a-f0-9])
pub fn is_hex_hash(s: &str) -> bool {
    !s.is_empty()
        && s.len() >= 4
        && s.len() <= 64
        && s.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

/// Checks if a string is alphanumeric (for [a-zA-Z0-9]+ pattern)
pub fn is_alnum_hash(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphanumeric())
}

/// Parses a hashed filename like "style.abcdef12.css" into (base, hash, ext).
/// Mirrors Go regex: `^(.+)\.([a-f0-9]{4,64})\.(css|js|jpg|jpeg|png|gif|svg|webp|ico)$`
pub fn parse_hashed_filename(filename: &str) -> Option<(String, String, String)> {
    let last_dot = filename.rfind('.')?;
    let ext = &filename[last_dot + 1..];
    if !HASH_EXTS.contains(&ext) {
        return None;
    }
    let before_ext = &filename[..last_dot];
    let second_dot = before_ext.rfind('.')?;
    let hash = &before_ext[second_dot + 1..];
    if !is_hex_hash(hash) {
        return None;
    }
    let base = &before_ext[..second_dot];
    if base.is_empty() {
        return None;
    }
    Some((base.to_string(), hash.to_string(), ext.to_string()))
}

/// Removes hash suffix from a basename: "style.abcdef12" -> "style"
/// Mirrors Go regex: `\.[a-f0-9]{4,64}$`
pub fn remove_hash_suffix(basename: &str) -> String {
    if let Some(dot_pos) = basename.rfind('.') {
        let after_dot = &basename[dot_pos + 1..];
        if is_hex_hash(after_dot) {
            return basename[..dot_pos].to_string();
        }
    }
    basename.to_string()
}

/// Collects all url() references from CSS content.
/// Returns (opening_quote, url, closing_quote) for each match.
/// Mirrors Go regex: `url\(\s*(['"]?)([^'")\s]+)(['"]?)\s*\)`
pub fn collect_css_urls(content: &str) -> Vec<(String, String, String)> {
    let mut results = Vec::new();
    let bytes = content.as_bytes();
    let mut pos = 0;

    while pos < bytes.len() {
        let remaining = &content[pos..];
        let lower = remaining.to_lowercase();
        let url_idx = match lower.find("url(") {
            Some(i) => pos + i,
            None => break,
        };

        let mut i = url_idx + 4;

        // Skip whitespace after "url("
        while i < bytes.len() && (bytes[i] == b' ' || bytes[i] == b'\t') {
            i += 1;
        }

        // Opening quote
        let oq = if i < bytes.len() && (bytes[i] == b'\'' || bytes[i] == b'"') {
            let q = bytes[i] as char;
            i += 1;
            q.to_string()
        } else {
            String::new()
        };

        // URL content (no quote, ), or whitespace)
        let url_start = i;
        while i < bytes.len() {
            let c = bytes[i];
            if c == b')'
                || c == b'\''
                || c == b'"'
                || c == b' '
                || c == b'\t'
                || c == b'\n'
                || c == b'\r'
            {
                break;
            }
            i += 1;
        }
        let url = content[url_start..i].to_string();

        // Closing quote
        let cq = if i < bytes.len() && (bytes[i] == b'\'' || bytes[i] == b'"') {
            let q = bytes[i] as char;
            i += 1;
            q.to_string()
        } else {
            String::new()
        };

        // Skip whitespace before )
        while i < bytes.len() && (bytes[i] == b' ' || bytes[i] == b'\t') {
            i += 1;
        }

        if i < bytes.len() && bytes[i] == b')' {
            i += 1;
            if !url.is_empty() {
                results.push((oq, url, cq));
            }
        }

        pos = i;
    }

    results
}

/// Replaces url() references in CSS content using a mapping.
/// Returns (new_content, was_updated).
/// Mirrors Go's updateCSSImageReferences logic.
pub fn replace_css_urls(content: &str, map: &HashMap<String, String>) -> (String, bool) {
    let urls = collect_css_urls(content);
    if urls.is_empty() {
        return (content.to_string(), false);
    }

    let mut result = content.to_string();
    let mut updated = false;

    for (oq, url, cq) in &urls {
        // Skip external/data URLs
        if url.starts_with("http") || url.starts_with("data:") || url.starts_with("//") {
            continue;
        }

        // Normalize: strip ?params, #fragments, convert \ to /
        let clean = url.split('?').next().unwrap_or(url);
        let clean = clean.split('#').next().unwrap_or(clean);
        let normalized = clean.replace('\\', "/");

        let new_filename = match map.get(&normalized) {
            Some(f) => f,
            None => continue,
        };

        // Build new path: dir + "/" + new_filename
        let dir = std::path::Path::new(&url)
            .parent()
            .and_then(|p| p.to_str())
            .unwrap_or("");
        let dir_norm = dir.replace('\\', "/");

        let new_path = if dir_norm == "." || dir_norm.is_empty() {
            new_filename.clone()
        } else {
            format!("{}/{}", dir_norm, new_filename)
        };

        // Fix mismatched quotes (Go logic)
        let (final_oq, final_cq) = if oq != cq {
            if !oq.is_empty() && cq.is_empty() {
                (oq.clone(), oq.clone())
            } else if oq.is_empty() && !cq.is_empty() {
                (cq.clone(), cq.clone())
            } else {
                (oq.clone(), cq.clone())
            }
        } else {
            (oq.clone(), cq.clone())
        };

        // More robust: find the exact match in result and replace
        let old_full = format!("url({}{}{})", oq, url, cq);
        let new_full = format!("url({}{}{})", final_oq, new_path, final_cq);

        if old_full != new_full {
            result = result.replacen(&old_full, &new_full, 1);
            updated = true;
        }
    }

    (result, updated)
}

/// Collects all href values from HTML link tags that match the given extension.
/// Mirrors Go regex: `<link[^>]*href\s*=\s*['"]([^'"]+\.css(?:\?[^'"]*)?)['"]`
pub fn collect_html_links(content: &str, ext: &str) -> Vec<String> {
    let mut results = Vec::new();
    let lower = content.to_lowercase();
    let mut search_pos = 0;

    while let Some(tag_start) = lower[search_pos..].find("<link") {
        let abs_start = search_pos + tag_start;
        // Find end of tag
        let tag_end = match content[abs_start..].find('>') {
            Some(e) => abs_start + e,
            None => break,
        };
        let tag = &content[abs_start..=tag_end];
        let tag_lower = tag.to_lowercase();

        // Find href= in the tag
        if let Some(href_pos) = tag_lower.find("href") {
            let mut i = href_pos + 4;
            // Skip whitespace
            while i < tag.len()
                && (tag.as_bytes()[i] == b' '
                    || tag.as_bytes()[i] == b'\t'
                    || tag.as_bytes()[i] == b'\n')
            {
                i += 1;
            }
            if i < tag.len() && tag.as_bytes()[i] == b'=' {
                i += 1;
                while i < tag.len() && (tag.as_bytes()[i] == b' ' || tag.as_bytes()[i] == b'\t') {
                    i += 1;
                }
                if i < tag.len() && (tag.as_bytes()[i] == b'\'' || tag.as_bytes()[i] == b'"') {
                    let quote = tag.as_bytes()[i];
                    i += 1;
                    let val_start = i;
                    while i < tag.len() && tag.as_bytes()[i] != quote {
                        i += 1;
                    }
                    let value = &tag[val_start..i];
                    let clean = value.split('?').next().unwrap_or(value);
                    if clean.to_lowercase().ends_with(&format!(".{}", ext)) {
                        results.push(value.to_string());
                    }
                }
            }
        }

        search_pos = tag_end + 1;
    }

    results
}

/// Collects all src values from HTML script tags that match .js extension.
/// Mirrors Go regex: `<script[^>]*src\s*=\s*['"]([^'"]+\.js(?:\?[^'"]*)?)['"]`
pub fn collect_html_scripts(content: &str) -> Vec<String> {
    let mut results = Vec::new();
    let lower = content.to_lowercase();
    let mut search_pos = 0;

    while let Some(tag_start) = lower[search_pos..].find("<script") {
        let abs_start = search_pos + tag_start;
        let tag_end = match content[abs_start..].find('>') {
            Some(e) => abs_start + e,
            None => break,
        };
        let tag = &content[abs_start..=tag_end];
        let tag_lower = tag.to_lowercase();

        if let Some(src_pos) = tag_lower.find("src") {
            let mut i = src_pos + 3;
            while i < tag.len()
                && (tag.as_bytes()[i] == b' '
                    || tag.as_bytes()[i] == b'\t'
                    || tag.as_bytes()[i] == b'\n')
            {
                i += 1;
            }
            if i < tag.len() && tag.as_bytes()[i] == b'=' {
                i += 1;
                while i < tag.len() && (tag.as_bytes()[i] == b' ' || tag.as_bytes()[i] == b'\t') {
                    i += 1;
                }
                if i < tag.len() && (tag.as_bytes()[i] == b'\'' || tag.as_bytes()[i] == b'"') {
                    let quote = tag.as_bytes()[i];
                    i += 1;
                    let val_start = i;
                    while i < tag.len() && tag.as_bytes()[i] != quote {
                        i += 1;
                    }
                    let value = &tag[val_start..i];
                    let clean = value.split('?').next().unwrap_or(value);
                    if clean.to_lowercase().ends_with(".js") {
                        results.push(value.to_string());
                    }
                }
            }
        }

        search_pos = tag_end + 1;
    }

    results
}

/// Removes HTML comments (non-greedy, multiline).
/// Mirrors Go regex: `(?s)<!--.*?-->`
pub fn remove_html_comments(content: &str) -> String {
    let mut result = String::with_capacity(content.len());
    let mut pos = 0;
    let bytes = content.as_bytes();

    while pos < bytes.len() {
        if pos + 3 < bytes.len() && &bytes[pos..pos + 4] == b"<!--" {
            // Find closing -->
            if let Some(end) = content[pos + 4..].find("-->") {
                pos = pos + 4 + end + 3;
            } else {
                result.push_str(&content[pos..]);
                break;
            }
        } else {
            result.push(bytes[pos] as char);
            pos += 1;
        }
    }

    result
}

/// Checks if a filename matches the pattern: basename.alphanumeric.ext
/// Used by cleanHashFiles and findAllFileVersions: `^basename\.[a-zA-Z0-9]+\.ext$`
pub fn matches_alnum_hash(filename: &str, basename: &str, ext: &str) -> bool {
    let prefix = format!("{}.", basename);
    let suffix = format!(".{}", ext);
    if filename.starts_with(&prefix) && filename.ends_with(&suffix) {
        let mid_end = filename.len().saturating_sub(suffix.len());
        if prefix.len() >= mid_end {
            return false;
        }
        let middle = &filename[prefix.len()..mid_end];
        return is_alnum_hash(middle);
    }
    false
}

/// Checks if a filename matches: basename.hexhash.ext (for findAndDeleteOldHashFiles)
/// Pattern: `^basename\.([a-f0-9]{4,64})ext$`
/// Note: ext includes the dot (e.g. ".css")
pub fn matches_hex_hash(filename: &str, basename: &str, ext_with_dot: &str) -> Option<String> {
    let prefix = format!("{}.", basename);
    if !filename.starts_with(&prefix) {
        return None;
    }
    if !filename.ends_with(ext_with_dot) {
        return None;
    }
    let mid_end = filename.len().saturating_sub(ext_with_dot.len());
    if prefix.len() >= mid_end {
        return None;
    }
    let middle = &filename[prefix.len()..mid_end];
    if is_hex_hash(middle) {
        Some(middle.to_string())
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_is_hex_hash() {
        assert!(is_hex_hash("abcdef12"));
        assert!(is_hex_hash("a1b2c3d4e5f6"));
        assert!(!is_hex_hash("abc")); // too short
        assert!(!is_hex_hash("xyz12345")); // non-hex
        assert!(!is_hex_hash("ABCDEF12")); // uppercase
    }

    #[test]
    fn test_parse_hashed_filename() {
        assert_eq!(
            parse_hashed_filename("style.abcdef12.css"),
            Some(("style".into(), "abcdef12".into(), "css".into()))
        );
        assert_eq!(
            parse_hashed_filename("app.min.12345678.js"),
            Some(("app.min".into(), "12345678".into(), "js".into()))
        );
        assert_eq!(parse_hashed_filename("style.css"), None);
        assert_eq!(parse_hashed_filename("style.abc.css"), None); // hash too short
    }

    #[test]
    fn test_remove_hash_suffix() {
        assert_eq!(remove_hash_suffix("style.abcdef12"), "style");
        assert_eq!(remove_hash_suffix("app.min.12345678"), "app.min");
        assert_eq!(remove_hash_suffix("style"), "style"); // no hash
    }

    #[test]
    fn test_collect_css_urls() {
        let css = r#"
            .bg1 { background: url('../images/bg.png'); }
            .bg2 { background-image: url("img/icon.svg?v=1"); }
            .bg3 { background: url(http://example.com/test.jpg); }
            .bg4 { background: url(data:image/png;base64,abc); }
        "#;
        let urls = collect_css_urls(css);
        assert_eq!(urls.len(), 4);
        assert_eq!(urls[0].1, "../images/bg.png");
        assert_eq!(urls[1].1, "img/icon.svg?v=1");
        assert_eq!(urls[2].1, "http://example.com/test.jpg");
    }

    #[test]
    fn test_replace_css_urls() {
        let css = r#"
            .bg1 { background: url('../images/bg.png'); }
            .bg2 { background-image: url("img/icon.svg?v=1"); }
        "#;
        let mut map = HashMap::new();
        map.insert(
            "../images/bg.png".to_string(),
            "bg.abcdef12.png".to_string(),
        );
        map.insert("img/icon.svg".to_string(), "icon.12345678.svg".to_string());

        let (result, updated) = replace_css_urls(css, &map);
        assert!(updated);
        assert!(result.contains("url('../images/bg.abcdef12.png')"));
        assert!(result.contains(r#"url("img/icon.12345678.svg")"#));
    }

    #[test]
    fn test_collect_html_links() {
        let html = r#"<link rel="stylesheet" href="css/style.css">
<link rel="stylesheet" href="css/index.css?v=2">
<script src="js/app.js"></script>"#;
        let links = collect_html_links(html, "css");
        assert_eq!(links.len(), 2);
        assert!(links.contains(&"css/style.css".to_string()));
        assert!(links.contains(&"css/index.css?v=2".to_string()));
    }

    #[test]
    fn test_collect_html_scripts() {
        let html = r#"<script src="js/app.js"></script>
<script src="js/index.js?v=2"></script>
<link href="css/style.css">"#;
        let scripts = collect_html_scripts(html);
        assert_eq!(scripts.len(), 2);
        assert!(scripts.contains(&"js/app.js".to_string()));
    }

    #[test]
    fn test_remove_html_comments() {
        let html = "<!-- comment --><div>hello</div><!-- another -->";
        let result = remove_html_comments(html);
        assert_eq!(result, "<div>hello</div>");
    }

    #[test]
    fn test_matches_hex_hash() {
        assert_eq!(
            matches_hex_hash("style.aaaabbbb.css", "style", ".css"),
            Some("aaaabbbb".to_string())
        );
        assert_eq!(
            matches_hex_hash("style.ccccdddd.css", "style", ".css"),
            Some("ccccdddd".to_string())
        );
        assert_eq!(matches_hex_hash("style.css", "style", ".css"), None);
        assert_eq!(
            matches_hex_hash("other.aaaabbbb.css", "style", ".css"),
            None
        );
    }

    #[test]
    fn test_matches_alnum_hash() {
        assert!(matches_alnum_hash("style.AAAA1111.css", "style", "css"));
        assert!(matches_alnum_hash("style.aaaabbbb.css", "style", "css"));
        assert!(!matches_alnum_hash("style.css", "style", "css"));
    }
}
