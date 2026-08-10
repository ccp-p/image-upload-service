//! Version management: file hashing, renaming, HTML/CSS resource processing.
//! Mirrors the Go VersionManager in cmd/hashCdn/main.go.

use std::collections::{HashMap, HashSet};
use std::path::PathBuf;

use crate::config::Config;
use crate::md5;
use crate::patterns;

use oxc_allocator::Allocator;
use oxc_codegen::{Codegen, CodegenOptions, CommentOptions};
use oxc_mangler::MangleOptions;
use oxc_minifier::{CompressOptions, Minifier, MinifierOptions};
use oxc_parser::Parser;
use oxc_span::SourceType;

// ---------------------------------------------------------------------------
// Structs
// ---------------------------------------------------------------------------

#[derive(Debug, Clone)]
pub struct FileInfo {
    pub original_path: String,
    pub hashed_path: String,
    pub hash: String,
    pub renamed: bool,
}

#[derive(Debug, Clone)]
pub struct ImageReference {
    pub original_path: String,
    pub absolute_path: String,
    pub relative_path: String,
}

pub struct VersionManager {
    pub config: Config,
    processed_files: HashMap<String, String>,
    pub debug_mode: bool,
    pub folder_opened: bool,
    pub commit_message: String,
    exclude_map: HashSet<String>,
}

// ---------------------------------------------------------------------------
// Free helper functions (mirror Go package-level helpers)
// ---------------------------------------------------------------------------

pub fn file_exists(path: &str) -> bool {
    std::fs::metadata(path).is_ok()
}

pub fn copy_file(src: &str, dst: &str) -> Result<(), String> {
    std::fs::copy(src, dst)
        .map(|_| ())
        .map_err(|e| e.to_string())
}

/// obfuscate_js minifies+mangles JS source via oxc toolchain.
/// Returns obfuscated bytes, or the original on error (non-fatal).
fn obfuscate_js(content: &[u8]) -> Vec<u8> {
    // oxc is well-tested (test262 conformance) but keep catch_unwind as safety net.
    let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        let source_text = std::str::from_utf8(content).unwrap_or("");
        let allocator = Allocator::default();
        let source_type = SourceType::default();
        let ret = Parser::new(&allocator, source_text, source_type).parse();
        if !ret.diagnostics.is_empty() {
            return Err(format!("parse error: {:?}", ret.diagnostics[0]));
        }
        let mut program = ret.program;
        let options = MinifierOptions {
            mangle: Some(MangleOptions::default()),
            compress: Some(CompressOptions::smallest()),
        };
        let ret = Minifier::new(options).minify(&allocator, &mut program);
        let codegen_ret = Codegen::new()
            .with_options(CodegenOptions {
                minify: true,
                comments: CommentOptions::disabled(),
                ..CodegenOptions::default()
            })
            .with_scoping(ret.scoping)
            .build(&program);
        Ok::<Vec<u8>, String>(codegen_ret.code.into_bytes())
    }));
    match result {
        Ok(Ok(bytes)) => bytes,
        Ok(Err(msg)) => {
            println!("  ⚠️  混淆失败: {}（使用原始内容）", msg);
            content.to_vec()
        }
        Err(_) => {
            println!("  ⚠️  混淆panic（使用原始内容）");
            content.to_vec()
        }
    }
}

/// hash_from_bytes computes MD5 from in-memory bytes, mirroring Go's hashFromBytes.
fn hash_from_bytes(data: &[u8], hash_length: usize) -> String {
    let full = md5::hex(data);
    if hash_length > 0 && hash_length < full.len() {
        full[..hash_length].to_string()
    } else {
        full
    }
}

/// Runs `git add <basename>` from the file's directory, mirroring Go's
/// vcsGitAdd. Best-effort and non-fatal: failures (e.g. not a git repo, or
/// git missing from PATH) only print a warning so hashing never aborts.
fn vcs_git_add(file_path: &str, debug_mode: bool) {
    let dir = path_dir(file_path);
    let base = path_base(file_path);
    // Use .output() (not .status()) so git's stderr is captured and never
    // leaks to the console, mirroring Go's CombinedOutput(). Failures are
    // non-fatal and only logged in debug mode, matching Go's vcsGitAdd.
    let output = std::process::Command::new("git")
        .arg("add")
        .arg(&base)
        .current_dir(&dir)
        .output();
   match output {
       Ok(s) if s.status.success() => {
           if debug_mode {
               println!("    ➕ Git add: {}", base);
           }
       }
       Ok(o) => {
           if debug_mode {
               let combined = format!(
                   "{}{}",
                   String::from_utf8_lossy(&o.stdout),
                   String::from_utf8_lossy(&o.stderr)
               );
              println!("      ⚠️  Git add 失败: {} ({})", base, o.status);
               println!("      Output: {}", combined.trim());
           }
       }
        Err(_) => {
            // git not in PATH — silently skip (mirrors Go's exec.LookPath guard)
        }
    }
}

/// Runs `svn delete --keep-local <basename>` from the file's directory,
/// mirroring Go's vcsSvnDelete. Best-effort and non-fatal: failures (e.g. not
/// an SVN working copy, or svn missing from PATH) only log a warning so the
/// delete flow never aborts. `--keep-local` leaves the file on disk so the
/// subsequent remove_file is what actually removes it.
pub fn vcs_svn_delete(file_path: &str, debug_mode: bool) {
    let dir = path_dir(file_path);
    let base = path_base(file_path);
    // Use .output() (not .status()) so svn's stderr is captured and never
    // leaks to the console, mirroring Go's CombinedOutput(). Failures are
    // non-fatal and only logged in debug mode, matching Go's vcsSvnDelete.
    let output = std::process::Command::new("svn")
        .arg("delete")
        .arg("--keep-local")
        .arg(&base)
        .current_dir(&dir)
        .output();
    match output {
        Ok(s) if s.status.success() => {
            if debug_mode {
                println!("    📝 SVN delete: {}", base);
            }
        }
        Ok(o) => {
            if debug_mode {
                let combined = format!(
                    "{}{}",
                    String::from_utf8_lossy(&o.stdout),
                    String::from_utf8_lossy(&o.stderr)
                );
                println!("      ⚠️  SVN delete 失败: {} ({})", base, o.status);
                println!("      Output: {}", combined.trim());
            }
        }
        Err(_) => {
            // svn not in PATH, silently skip (mirrors Go's exec.LookPath guard)
        }
    }
}

/// Full MD5 hex of a file's contents (no truncation).
pub fn get_file_hash(file_path: &str) -> Result<String, String> {
    let data = std::fs::read(file_path).map_err(|e| e.to_string())?;
    Ok(md5::hex(&data))
}

pub fn is_js_or_css(filename: &str) -> bool {
    let lower = filename.to_lowercase();
    lower.ends_with(".js") || lower.ends_with(".css")
}

/// Returns the file extension including the leading dot (Go's filepath.Ext).
fn get_ext(filename: &str) -> String {
    match filename.rfind('.') {
        Some(pos) => filename[pos..].to_string(),
        None => String::new(),
    }
}

/// Strips the extension from a filename (Go's strings.TrimSuffix(name, ext)).
fn get_basename(filename: &str) -> String {
    let ext = get_ext(filename);
    if !ext.is_empty() && filename.ends_with(&ext) {
        filename[..filename.len() - ext.len()].to_string()
    } else {
        filename.to_string()
    }
}

/// Returns the last path component (Go's filepath.Base).
fn path_base(path: &str) -> String {
    match path.rfind(|c: char| c == '/' || c == '\\') {
        Some(p) => path[p + 1..].to_string(),
        None => path.to_string(),
    }
}

/// Returns the directory portion (Go's filepath.Dir).
fn path_dir(path: &str) -> String {
    match path.rfind(|c: char| c == '/' || c == '\\') {
        Some(0) => ".".to_string(),
        Some(p) => path[..p].to_string(),
        None => ".".to_string(),
    }
}

/// Joins two path components (Go's filepath.Join).
fn path_join(dir: &str, name: &str) -> String {
    let name = name.trim_start_matches(|c| c == '/' || c == '\\');
    PathBuf::from(dir).join(name).to_string_lossy().to_string()
}

/// Relative path from base to target (simplified filepath.Rel).
fn path_relative(base: &str, target: &str) -> String {
    let base = base.trim_end_matches(|c: char| c == '/' || c == '\\');
    let target = target.trim_end_matches(|c: char| c == '/' || c == '\\');
    if target.starts_with(base) {
        target[base.len()..]
            .trim_start_matches(|c: char| c == '/' || c == '\\')
            .to_string()
    } else {
        target.to_string()
    }
}

/// Returns current local time as "YYYY-MM-DD HH:MM:SS" (mirrors Go's time.Now().Format).
#[cfg(windows)]
fn format_now() -> String {
    #[repr(C)]
    struct LocalSystemTime {
        year: u16,
        month: u16,
        _day_of_week: u16,
        day: u16,
        hour: u16,
        minute: u16,
        second: u16,
        _milliseconds: u16,
    }
    extern "system" {
        fn GetLocalTime(systime: *mut LocalSystemTime);
    }
    unsafe {
        let mut st = LocalSystemTime {
            year: 0,
            month: 0,
            _day_of_week: 0,
            day: 0,
            hour: 0,
            minute: 0,
            second: 0,
            _milliseconds: 0,
        };
        GetLocalTime(&mut st);
        format!(
            "{:04}-{:02}-{:02} {:02}:{:02}:{:02}",
            st.year, st.month, st.day, st.hour, st.minute, st.second
        )
    }
}

#[cfg(not(windows))]
fn format_now() -> String {
    let secs = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64;
    let days = secs.div_euclid(86400);
    let rem = secs.rem_euclid(86400);
    let z = days + 719468;
    let era = z.div_euclid(146097);
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = if m <= 2 { y + 1 } else { y };
    format!(
        "{:04}-{:02}-{:02} {:02}:{:02}:{:02}",
        year,
        m,
        d,
        rem / 3600,
        (rem % 3600) / 60,
        rem % 60
    )
}

/// File modification time in nanoseconds since UNIX epoch.
fn mod_time_nanos(metadata: &std::fs::Metadata) -> i64 {
    metadata
        .modified()
        .ok()
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map(|d| d.as_nanos() as i64)
        .unwrap_or(0)
}

/// Checks if a 19-char string matches "YYYY-MM-DD HH:MM:SS".
fn is_date_time_format(s: &str) -> bool {
    let b = s.as_bytes();
    b.len() >= 19
        && b[0].is_ascii_digit()
        && b[1].is_ascii_digit()
        && b[2].is_ascii_digit()
        && b[3].is_ascii_digit()
        && b[4] == b'-'
        && b[5].is_ascii_digit()
        && b[6].is_ascii_digit()
        && b[7] == b'-'
        && b[8].is_ascii_digit()
        && b[9].is_ascii_digit()
        && (b[10] == b' ' || b[10] == b'\t')
        && b[11].is_ascii_digit()
        && b[12].is_ascii_digit()
        && b[13] == b':'
        && b[14].is_ascii_digit()
        && b[15].is_ascii_digit()
        && b[16] == b':'
        && b[17].is_ascii_digit()
        && b[18].is_ascii_digit()
}

/// Updates @create/@modify date patterns in HTML comments (mirrors Go's updateHTMLContent).
fn update_comment_dates(content: &str) -> (String, bool) {
    let now = format_now();
    let mut result = content.to_string();
    let mut updated = false;
    for keyword in &["@create date ", "@modify date "] {
        let mut search_from = 0;
        while let Some(idx) = result[search_from..].find(keyword) {
            let abs_idx = search_from + idx;
            let date_start = abs_idx + keyword.len();
            if date_start + 19 <= result.len() {
                let date_str = &result[date_start..date_start + 19];
                if is_date_time_format(date_str) {
                    let before = &result[..date_start];
                    let after = &result[date_start + 19..];
                    result = format!("{}{}{}", before, now, after);
                    updated = true;
                    search_from = date_start + now.len();
                } else {
                    search_from = abs_idx + 1;
                }
            } else {
                search_from = abs_idx + 1;
            }
        }
    }
    if updated {
        println!("  🕐 注释日期已更新: {}", now);
    }
    (result, updated)
}

/// Matches `^nameWithoutExt\.[a-f0-9]{8}ext$` (Go findFile pattern).
fn matches_hex_hash_exact(filename: &str, name_without_ext: &str, ext: &str) -> bool {
    let prefix = format!("{}.", name_without_ext);
    if !filename.starts_with(&prefix) || !filename.ends_with(ext) {
        return false;
    }
    let middle = &filename[prefix.len()..filename.len() - ext.len()];
    middle.len() == 8
        && middle
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

// ---------------------------------------------------------------------------
// VersionManager implementation
// ---------------------------------------------------------------------------

impl VersionManager {
    pub fn new(config: Config, debug_mode: bool) -> Self {
        let exclude_map = config.cdn_exclude_files.iter().cloned().collect();
        VersionManager {
            config,
            processed_files: HashMap::new(),
            debug_mode,
            folder_opened: false,
            commit_message: String::new(),
            exclude_map,
        }
    }

    pub fn should_process_component(&self, component_path: &str) -> bool {
        if self.config.include_components.is_empty() {
            return true;
        }
        for name in &self.config.include_components {
            if component_path.contains(&format!("/{}/", name))
                || component_path.contains(&format!("\\{}\\", name))
                || component_path.ends_with(&format!("/{}", name))
                || component_path.ends_with(&format!("\\{}", name))
                || path_base(component_path).starts_with(&format!("{}.", name))
            {
                return true;
            }
        }
        false
    }

    pub fn calculate_file_hash(&self, file_path: &str) -> Result<String, String> {
        let data = std::fs::read(file_path).map_err(|e| e.to_string())?;
        let full = md5::hex(&data);
        if self.config.hash_length > 0 && self.config.hash_length < full.len() {
            Ok(full[..self.config.hash_length].to_string())
        } else {
            Ok(full)
        }
    }

    pub fn remove_hash_from_filename(&self, filename: &str) -> String {
        if let Some((base, _hash, ext)) = patterns::parse_hashed_filename(filename) {
            format!("{}.{}", base, ext)
        } else {
            filename.to_string()
        }
    }

    pub fn add_hash_to_filename(&self, filename: &str, hash: &str) -> String {
        let ext = get_ext(filename);
        let basename = if ext.is_empty() {
            filename.to_string()
        } else {
            filename[..filename.len() - ext.len()].to_string()
        };
        let clean = patterns::remove_hash_suffix(&basename);
        if ext.is_empty() {
            format!("{}.{}", clean, hash)
        } else {
            format!("{}.{}{}", clean, hash, ext)
        }
    }

    pub fn find_and_delete_old_hash_files(
        &self,
        dir: &str,
        basename: &str,
        ext: &str,
        current_hash: &str,
    ) -> Result<(), String> {
        let entries = std::fs::read_dir(dir).map_err(|e| e.to_string())?;
        for entry in entries.flatten() {
            let filename = entry.file_name().to_string_lossy().to_string();
            if let Some(hash) = patterns::matches_hex_hash(&filename, basename, ext) {
                if hash != current_hash {
                    let old_path = path_join(dir, &filename);
                    vcs_svn_delete(&old_path, self.debug_mode);
                    let _ = std::fs::remove_file(&old_path);
                    if is_js_or_css(&filename) {
                        println!("    🗑️  已删除: {}", filename);
                    }
                }
            }
        }
        Ok(())
    }

    pub fn rename_file_with_hash(&self, file_path: &str) -> Result<FileInfo, String> {
        let dir = path_dir(file_path);
        let filename = path_base(file_path);
        let clean_filename = self.remove_hash_from_filename(&filename);

       let clean_path = path_join(&dir, &clean_filename);
       let source_path = if file_exists(&clean_path) {
           clean_path
       } else {
           file_path.to_string()
       };

        // Experimental: obfuscate (minify+mangle) JS files before hashing
        let is_js = clean_filename.to_lowercase().ends_with(".js");
        let processed_content: Option<Vec<u8>> = if self.config.obfuscate_js && is_js {
            let raw = std::fs::read(&source_path).map_err(|e| e.to_string())?;
            let obf = obfuscate_js(&raw);
            println!(
                "  🔒 混淆: {} ({} -> {} bytes)",
                clean_filename,
                raw.len(),
                obf.len()
            );
            Some(obf)
        } else {
            None
        };

        let hash = match &processed_content {
            Some(content) => hash_from_bytes(content, self.config.hash_length),
            None => self.calculate_file_hash(&source_path)?,
        };
       let new_filename = self.add_hash_to_filename(&clean_filename, &hash);
       let new_path = path_join(&dir, &new_filename);

       let info = FileInfo {
           original_path: source_path.clone(),
           hashed_path: new_path.clone(),
           hash: hash.clone(),
           renamed: true,
       };

       if file_exists(&new_path) {
           let ext = get_ext(&clean_filename);
           let bn = get_basename(&clean_filename);
           let _ = self.find_and_delete_old_hash_files(&dir, &bn, &ext, &hash);
           return Ok(info);
       }

        if let Some(content) = &processed_content {
            std::fs::write(&new_path, content).map_err(|e| e.to_string())?;
        } else {
            copy_file(&source_path, &new_path)?;
        }
       vcs_git_add(&new_path, self.debug_mode);
        if is_js_or_css(&new_filename) {
            println!("  ✅ 已生成: {}", new_filename);
        }

        let ext = get_ext(&clean_filename);
        let bn = get_basename(&clean_filename);
        let _ = self.find_and_delete_old_hash_files(&dir, &bn, &ext, &hash);

        Ok(info)
    }

    pub fn collect_images_from_css(&self, css_path: &str) -> Result<Vec<ImageReference>, String> {
        let content = std::fs::read_to_string(css_path).map_err(|e| e.to_string())?;
        let css_dir = path_dir(css_path);
        let urls = patterns::collect_css_urls(&content);
        let mut images = Vec::new();
        for (_oq, url, _cq) in &urls {
            if url.starts_with("http") || url.starts_with("data:") || url.starts_with("//") {
                continue;
            }
            let clean = url.split('?').next().unwrap_or(url);
            let clean = clean.split('#').next().unwrap_or(clean);
            let absolute = PathBuf::from(&css_dir)
                .join(clean.replace('\\', "/"))
                .to_string_lossy()
                .to_string();
            if file_exists(&absolute) {
                let rel = path_relative(&css_dir, &absolute);
                images.push(ImageReference {
                    original_path: clean.to_string(),
                    absolute_path: absolute,
                    relative_path: rel,
                });
            }
        }
        Ok(images)
    }

    pub fn update_css_image_references(
        &self,
        css_path: &str,
        image_map: &HashMap<String, String>,
    ) -> Result<(), String> {
        let content = std::fs::read_to_string(css_path).map_err(|e| e.to_string())?;
        let (new_content, updated) = patterns::replace_css_urls(&content, image_map);
        if updated {
            std::fs::write(css_path, new_content.as_bytes()).map_err(|e| e.to_string())?;
        }
        Ok(())
    }

    pub fn find_file(&self, base_path: &str) -> String {
        if file_exists(base_path) {
            return base_path.to_string();
        }

        let dir = path_dir(base_path);
        let name = path_base(base_path);
        let ext = get_ext(&name);
        let name_without_ext = if ext.is_empty() {
            name.clone()
        } else {
            name[..name.len() - ext.len()].to_string()
        };

        if !file_exists(&dir) {
            return String::new();
        }

        let entries = match std::fs::read_dir(&dir) {
            Ok(e) => e,
            Err(_) => return String::new(),
        };

        for entry in entries.flatten() {
            let filename = entry.file_name().to_string_lossy().to_string();
            if matches_hex_hash_exact(&filename, &name_without_ext, &ext) {
                return path_join(&dir, &filename);
            }
        }
        String::new()
    }

    pub fn collect_resources_from_html(
        &self,
        html_path: &str,
    ) -> Result<HashMap<String, Vec<String>>, String> {
        let content = std::fs::read_to_string(html_path).map_err(|e| e.to_string())?;
        let html_basename = {
            let base = path_base(html_path);
            if base.ends_with(".html") {
                base[..base.len() - 5].to_string()
            } else {
                base
            }
        };

        let should_process_main = self
            .config
            .process_main_resources
            .iter()
            .any(|name| *name == path_base(html_path) || *name == html_basename);

        let mut resources = HashMap::new();
        resources.insert("css".to_string(), Vec::new());
        resources.insert("js".to_string(), Vec::new());

        for css_path in patterns::collect_html_links(&content, "css") {
            let css_path = css_path.split('?').next().unwrap_or(&css_path).to_string();
            let is_external = css_path.starts_with("http") || css_path.starts_with("//");
            if is_external {
                if should_process_main || !css_path.contains("components") {
                    continue;
                }
            } else if !css_path.contains("components") {
                continue;
            }
            if !self.should_process_component(&css_path) {
                continue;
            }
            resources.get_mut("css").unwrap().push(css_path);
        }

        for js_path in patterns::collect_html_scripts(&content) {
            let js_path = js_path.split('?').next().unwrap_or(&js_path).to_string();
            let is_external = js_path.starts_with("http") || js_path.starts_with("//");
            if is_external {
                if should_process_main || !js_path.contains("components") {
                    continue;
                }
            } else if !js_path.contains("components") {
                continue;
            }
            if !self.should_process_component(&js_path) {
                continue;
            }
            resources.get_mut("js").unwrap().push(js_path);
        }

        Ok(resources)
    }

    pub fn update_html_content(
        &self,
        html_path: &str,
        resources: &HashMap<String, HashMap<String, String>>,
    ) -> Result<(), String> {
        let content = std::fs::read_to_string(html_path).map_err(|e| e.to_string())?;
        let mut content_str = content;
        let mut updated = false;

        if let Some(css_map) = resources.get("css") {
            let (new_content, was_updated) = apply_resource_to_tags(
                &content_str,
                "link",
                "href",
                css_map,
                &self.config.cdn_domain,
                &self.exclude_map,
            );
            content_str = new_content;
            if was_updated {
                updated = true;
            }
        }

        if let Some(js_map) = resources.get("js") {
            let (new_content, was_updated) = apply_resource_to_tags(
                &content_str,
                "script",
                "src",
                js_map,
                &self.config.cdn_domain,
                &self.exclude_map,
            );
            content_str = new_content;
            if was_updated {
                updated = true;
            }
        }

        if !self.config.cdn_domain.is_empty() {
            let (nc, u1) = apply_generic_cdn(
                &content_str,
                "link",
                "href",
                "css",
                &self.config.cdn_domain,
                &self.exclude_map,
            );
            content_str = nc;
            if u1 {
                updated = true;
            }
            let (nc, u2) = apply_generic_cdn(
                &content_str,
                "script",
                "src",
                "js",
                &self.config.cdn_domain,
                &self.exclude_map,
            );
            content_str = nc;
            if u2 {
                updated = true;
            }
        }

        let (date_content, date_updated) = update_comment_dates(&content_str);
        content_str = date_content;
        if date_updated {
            updated = true;
        }

        if updated {
            std::fs::write(html_path, content_str.as_bytes()).map_err(|e| e.to_string())?;
        }
        Ok(())
    }

    pub fn find_all_html_files(&self) -> Vec<String> {
        let exclude: HashSet<String> = self.config.exclude_dirs.iter().cloned().collect();
        let mut files = Vec::new();
        walk_for_html(
            &self.config.root_dir,
            &exclude,
            &mut files,
            &self.config.root_dir,
        );
        files
    }

    pub fn should_exclude_from_cdn(&self, file_path: &str) -> bool {
        should_exclude_cdn(file_path, &self.exclude_map)
    }

    // -- Full workflow methods (used by CLI) --------------------------------

    pub fn process_component_resource(
        &mut self,
        html_dir: &str,
        relative_path: &str,
    ) -> Result<FileInfo, String> {
        let mut target = relative_path.to_string();
        if target.starts_with("http") || target.starts_with("//") {
            if let Some(idx) = target.find("components/") {
                target = target[idx..].to_string();
            }
        }
        if let Some(idx) = target.find('?') {
            target = target[..idx].to_string();
        }

        let absolute = path_join(html_dir, &target.replace('\\', "/"));
        let actual = {
            let found = self.find_file(&absolute);
            if found.is_empty() {
                absolute
            } else {
                found
            }
        };

        if !file_exists(&actual) {
            return Err(format!("file not found: {}", actual));
        }

        if let Some(cached) = self.processed_files.get(&actual).cloned() {
            if !cached.is_empty() {
                let dir = path_dir(&actual);
                let filename = path_base(&actual);
                let clean = self.remove_hash_from_filename(&filename);
                let hashed = self.add_hash_to_filename(&clean, &cached);
                return Ok(FileInfo {
                    original_path: actual,
                    hashed_path: path_join(&dir, &hashed),
                    hash: cached,
                    renamed: true,
                });
            }
        }
        self.processed_files.insert(actual.clone(), String::new());

        if actual.to_lowercase().ends_with(".css") {
            let info = self.process_component_css(&actual)?;
            self.processed_files.insert(actual, info.hash.clone());
            Ok(info)
        } else {
            let info = self.rename_file_with_hash(&actual)?;
            self.processed_files.insert(actual, info.hash.clone());
            Ok(info)
        }
    }

    pub fn process_component_css(&mut self, css_path: &str) -> Result<FileInfo, String> {
        let css_dir = path_dir(css_path);
        let filename = path_base(css_path);
        let clean_filename = self.remove_hash_from_filename(&filename);

        let original_css = {
            let p = path_join(&css_dir, &clean_filename);
            if file_exists(&p) {
                p
            } else {
                css_path.to_string()
            }
        };

        let images = self.collect_images_from_css(&original_css)?;
        if !images.is_empty() {
            println!("    📸 处理 {} 个图片引用", images.len());
        }
        let mut image_map: HashMap<String, String> = HashMap::new();

        for image in &images {
            let key = image.original_path.replace('\\', "/");

            if let Some(cached) = self.processed_files.get(&image.absolute_path).cloned() {
                if !cached.is_empty() {
                    let dir = path_dir(&image.absolute_path);
                    let clean_img =
                        self.remove_hash_from_filename(&path_base(&image.absolute_path));
                    let new_img = self.add_hash_to_filename(&clean_img, &cached);
                    let hp = path_join(&dir, &new_img);
                    if file_exists(&hp) {
                        image_map.insert(key, new_img);
                    } else {
                        let found = self.find_file(&path_join(&dir, &clean_img));
                        if !found.is_empty() {
                            image_map.insert(key, path_base(&found));
                        }
                    }
                    continue;
                }
            }
            self.processed_files
                .insert(image.absolute_path.clone(), String::new());

            if let Ok(info) = self.rename_file_with_hash(&image.absolute_path) {
                let new_img = path_base(&info.hashed_path);
                image_map.insert(key, new_img);
                self.processed_files
                    .insert(image.absolute_path.clone(), info.hash.clone());
            }
        }

        let original_hash = self.calculate_file_hash(&original_css)?;
        let hashed_name = self.add_hash_to_filename(&clean_filename, &original_hash);
        let mut hashed_path = path_join(&css_dir, &hashed_name);
        let mut final_hash = original_hash.clone();

        copy_file(&original_css, &hashed_path)?;

        if !image_map.is_empty() {
            self.update_css_image_references(&hashed_path, &image_map)?;
            let new_hash = self.calculate_file_hash(&hashed_path)?;
            if new_hash != original_hash {
                let final_name = self.add_hash_to_filename(&clean_filename, &new_hash);
                let final_path = path_join(&css_dir, &final_name);
                if final_path != hashed_path {
                    std::fs::rename(&hashed_path, &final_path).map_err(|e| e.to_string())?;
                    hashed_path = final_path;
                    final_hash = new_hash;
                }
            }
        }

        vcs_git_add(&hashed_path, self.debug_mode);

        let css_ext = get_ext(&clean_filename);
        let css_bn = get_basename(&clean_filename);
        let _ = self.find_and_delete_old_hash_files(&css_dir, &css_bn, &css_ext, &final_hash);

        Ok(FileInfo {
            original_path: original_css,
            hashed_path,
            hash: final_hash,
            renamed: true,
        })
    }

    pub fn process_html_file(&mut self, html_path: &str) -> Result<(), String> {
        if !file_exists(html_path) {
            return Err(format!("file not found: {}", html_path));
        }

        let html_dir = path_dir(html_path);
        let html_bn = {
            let base = path_base(html_path);
            if base.ends_with(".html") {
                base[..base.len() - 5].to_string()
            } else {
                base
            }
        };

        let should_process_main = self
            .config
            .process_main_resources
            .iter()
            .any(|name| *name == path_base(html_path) || *name == html_bn);

        let mut resources: HashMap<String, HashMap<String, String>> = HashMap::new();
        resources.insert("css".to_string(), HashMap::new());
        resources.insert("js".to_string(), HashMap::new());

        println!("============================================================");
        println!("📄 处理: {}", html_path);
        println!("============================================================");
        if should_process_main {
            println!("🎯 策略: 处理主资源 (JS/CSS) 及组件");
        } else {
            println!("🎯 策略: 处理组件资源");
        }
        println!();

        if should_process_main {
            println!("📦 处理主 JavaScript 文件...");
            let js_candidates = [
                path_join(&html_dir, &format!("{}.js", html_bn)),
                path_join(&html_dir, &format!("js/{}.js", html_bn)),
                path_join(&html_dir, &format!("scripts/js/{}.js", html_bn)),
            ];
            for js_path in &js_candidates {
                let actual = self.find_file(js_path);
                if !actual.is_empty() {
                    if let Ok(info) = self.rename_file_with_hash(&actual) {
                        println!("  ✅ JS: {} -> {}", path_base(&actual), path_base(&info.hashed_path));
                        let rel = path_relative(&html_dir, &actual).replace('\\', "/");
                        let hrel = path_relative(&html_dir, &info.hashed_path).replace('\\', "/");
                        let key = rel.strip_prefix("./").unwrap_or(&rel).to_string();
                        resources.get_mut("js").unwrap().entry(key).or_insert(hrel);
                    }
                    break;
                }
            }

            println!();
            println!("🎨 处理主 CSS 文件...");
            let css_candidates = [
                path_join(&html_dir, &format!("{}.css", html_bn)),
                path_join(&html_dir, &format!("css/{}.css", html_bn)),
            ];
            for css_path in &css_candidates {
                let actual = self.find_file(css_path);
                if !actual.is_empty() {
                    if let Ok(info) = self.process_component_css(&actual) {
                        println!("  ✅ CSS: {} -> {}", path_base(&actual), path_base(&info.hashed_path));
                        let rel = path_relative(&html_dir, &actual).replace('\\', "/");
                        let hrel = path_relative(&html_dir, &info.hashed_path).replace('\\', "/");
                        let key = rel.strip_prefix("./").unwrap_or(&rel).to_string();
                        resources.get_mut("css").unwrap().entry(key).or_insert(hrel);
                    }
                   break;
               }
           }

           // Extra hash resources: shared scripts (e.g. utils_index.js) referenced
           // by the page but outside a "components" path, so collect_resources skips them.
           if !self.config.extra_hash_resources.is_empty() {
               println!();
               println!("📦 处理额外 hash 资源...");
               for rel_path in &self.config.extra_hash_resources {
                   let clean_rel = rel_path.replace('\\', "/");
                   let abs = path_join(&html_dir, &clean_rel);
                   let actual = self.find_file(&abs);
                   let target = if actual.is_empty() { abs } else { actual };
                   if !file_exists(&target) {
                       eprintln!("  ⚠️ 未找到: {}", clean_rel);
                       continue;
                   }
                   let rel = path_relative(&html_dir, &target).replace('\\', "/");
                   let key = rel.strip_prefix("./").unwrap_or(&rel).to_string();
                   if resources.get("js").unwrap().contains_key(&key) {
                       continue;
                   }
                   match self.rename_file_with_hash(&target) {
                       Ok(info) => {
                           println!("  ✅ JS: {} -> {}", path_base(&target), path_base(&info.hashed_path));
                           let hrel = path_relative(&html_dir, &info.hashed_path).replace('\\', "/");
                           resources.get_mut("js").unwrap().insert(key, hrel);
                       }
                       Err(e) => eprintln!("  ⚠️ 处理失败 {}: {}", clean_rel, e),
                   }
               }
           }
       }

       println!();
       println!("🔍 扫描组件资源...");
        if let Ok(html_resources) = self.collect_resources_from_html(html_path) {
            let js_count = html_resources.get("js").map(|v| v.len()).unwrap_or(0);
            let css_count = html_resources.get("css").map(|v| v.len()).unwrap_or(0);
            println!("  找到 {} 个组件CSS, {} 个组件JS", css_count, js_count);
            if let Some(js_list) = html_resources.get("js") {
                if js_count > 0 {
                    println!();
                    println!("🔧 处理组件 JavaScript 文件...");
                }
                for js_rel in js_list {
                    let key = js_rel.replace('\\', "/");
                    let key = key.strip_prefix("./").unwrap_or(&key).to_string();
                    if resources.get("js").unwrap().contains_key(&key) {
                        continue;
                    }
                    if let Ok(info) = self.process_component_resource(&html_dir, js_rel) {
                        let hrel = path_relative(&html_dir, &info.hashed_path).replace('\\', "/");
                        resources.get_mut("js").unwrap().insert(key, hrel);
                    }
                }
            }
            if let Some(css_list) = html_resources.get("css") {
                if css_count > 0 {
                    println!();
                    println!("🔧 处理组件 CSS 文件...");
                }
                for css_rel in css_list {
                    let key = css_rel.replace('\\', "/");
                    let key = key.strip_prefix("./").unwrap_or(&key).to_string();
                    if resources.get("css").unwrap().contains_key(&key) {
                        continue;
                    }
                    if let Ok(info) = self.process_component_resource(&html_dir, css_rel) {
                        let hrel = path_relative(&html_dir, &info.hashed_path).replace('\\', "/");
                        resources.get_mut("css").unwrap().insert(key, hrel);
                    }
                }
            }
        }

        println!();
        println!("🔄 更新HTML中的资源引用...");
        println!(
            "  📋 CSS: {} 项, JS: {} 项",
            resources.get("css").unwrap().len(),
            resources.get("js").unwrap().len()
        );
        self.update_html_content(html_path, &resources)?;
        println!("✅ HTML文件已更新");
        println!();
        Ok(())
    }

    pub fn process_multiple_html_files(&mut self, html_paths: &[String]) {
        for html_path in html_paths {
            let abs = path_join(&self.config.root_dir, html_path);
            if let Err(e) = self.process_html_file(&abs) {
                eprintln!("error processing {}: {}", html_path, e);
            }
        }
    }
}

// ---------------------------------------------------------------------------
// HTML processing helpers
// ---------------------------------------------------------------------------

struct AttrMatch {
    val_start: usize,
    val_end: usize,
    value: String,
}

fn find_attr_value(tag: &str, tag_lower: &str, attr: &str) -> Option<AttrMatch> {
    let attr_lower = attr.to_lowercase();
    let bytes = tag.as_bytes();
    let mut search = 0;
    while let Some(rel) = tag_lower[search..].find(&attr_lower) {
        let abs_attr = search + rel;
        if abs_attr > 0 {
            let prev = bytes[abs_attr - 1];
            if prev != b' ' && prev != b'\t' && prev != b'\n' && prev != b'\r' {
                search = abs_attr + attr_lower.len();
                continue;
            }
        }
        let mut i = abs_attr + attr_lower.len();
        while i < bytes.len() && matches!(bytes[i], b' ' | b'\t' | b'\n') {
            i += 1;
        }
        if i >= bytes.len() || bytes[i] != b'=' {
            search = abs_attr + attr_lower.len();
            continue;
        }
        i += 1;
        while i < bytes.len() && matches!(bytes[i], b' ' | b'\t') {
            i += 1;
        }
        if i >= bytes.len() || (bytes[i] != b'\'' && bytes[i] != b'"') {
            search = abs_attr + attr_lower.len();
            continue;
        }
        let quote = bytes[i];
        i += 1;
        let val_start = i;
        while i < bytes.len() && bytes[i] != quote {
            i += 1;
        }
        if i >= bytes.len() {
            search = abs_attr + attr_lower.len();
            continue;
        }
        return Some(AttrMatch {
            val_start,
            val_end: i,
            value: tag[val_start..i].to_string(),
        });
    }
    None
}

fn replace_attr_in_tags(
    content: &str,
    tag: &str,
    attr: &str,
    old_path: &str,
    new_path: &str,
) -> (String, bool) {
    let lower = content.to_lowercase();
    let tag_lower = format!("<{}", tag);
    let mut result = String::with_capacity(content.len());
    let mut pos = 0;
    let mut updated = false;

    while pos < content.len() {
        let rel = match lower[pos..].find(&tag_lower) {
            Some(r) => pos + r,
            None => {
                result.push_str(&content[pos..]);
                break;
            }
        };
        result.push_str(&content[pos..rel]);
        let tag_end = match content[rel..].find('>') {
            Some(e) => rel + e,
            None => {
                result.push_str(&content[rel..]);
                return (result, updated);
            }
        };
        let tag_content = &content[rel..=tag_end];
        let tag_lower_slice = &lower[rel..=tag_end];

        let replaced = if let Some(am) = find_attr_value(tag_content, tag_lower_slice, attr) {
            let clean_value = am.value.split('?').next().unwrap_or(&am.value).to_string();
            let prefix = if clean_value.starts_with("./") {
                "./"
            } else if clean_value.starts_with("../") {
                "../"
            } else {
                ""
            };
            let stripped = if prefix.is_empty() {
                clean_value.as_str()
            } else {
                &clean_value[prefix.len()..]
            };
            if stripped == old_path {
                // Don't re-prepend ./ or ../ when the replacement is already an
                // absolute/CDN URL — otherwise we get "./https://..." which the
                // generic CDN pass then double-prefixes.
                let final_new = if prefix.is_empty()
                    || new_path.starts_with(prefix)
                    || new_path.starts_with("http://")
                    || new_path.starts_with("https://")
                    || new_path.starts_with("//")
                {
                    new_path.to_string()
                } else {
                    format!("{}{}", prefix, new_path)
                };
                let new_tag = format!(
                    "{}{}{}",
                    &tag_content[..am.val_start],
                    final_new,
                    &tag_content[am.val_end..]
                );
                result.push_str(&new_tag);
                updated = true;
                true
            } else {
                false
            }
        } else {
            false
        };
        if !replaced {
            result.push_str(tag_content);
        }
        pos = tag_end + 1;
    }
    (result, updated)
}

fn apply_resource_to_tags(
    content: &str,
    tag: &str,
    attr: &str,
    map: &HashMap<String, String>,
    cdn_domain: &str,
    exclude_map: &HashSet<String>,
) -> (String, bool) {
    let mut result = content.to_string();
    let mut updated = false;

    for (original_rel, new_hashed) in map {
        let old_dir = path_dir(original_rel);
        let new_filename = path_base(new_hashed);
        let is_url = original_rel.starts_with("http") || original_rel.starts_with("//");

        let mut new_path = if is_url {
            let last_slash = original_rel.rfind('/').unwrap_or(0);
            if last_slash > 0 {
                format!("{}/{}", &original_rel[..last_slash], new_filename)
            } else {
                new_filename.clone()
            }
        } else if old_dir != "." && old_dir != "/" {
            path_join(&old_dir, &new_filename).replace('\\', "/")
        } else {
            new_filename.clone()
        };

        if !cdn_domain.is_empty()
            && !new_path.starts_with("http")
            && !should_exclude_cdn(&new_path, exclude_map)
        {
            let mut clean = new_path.clone();
            if clean.starts_with("./") {
                clean = clean[2..].to_string();
            } else if clean.starts_with("../") {
                clean = clean[3..].to_string();
            }
            new_path = format!("{}/{}", cdn_domain, clean);
        }

        let (nc, u) = replace_attr_in_tags(&result, tag, attr, original_rel, &new_path);
        result = nc;
        if u {
            updated = true;
            let res_type = if tag == "link" { "CSS" } else { "JS" };
            println!(
                "  ✅ {}: {} -> {}",
                res_type,
                path_base(original_rel),
                path_base(&new_path)
            );
        }
    }
    (result, updated)
}

fn apply_generic_cdn(
    content: &str,
    tag: &str,
    attr: &str,
    ext_check: &str,
    cdn_domain: &str,
    exclude_map: &HashSet<String>,
) -> (String, bool) {
    if cdn_domain.is_empty() {
        return (content.to_string(), false);
    }
    let lower = content.to_lowercase();
    let tag_lower = format!("<{}", tag);
    let ext_lower = format!(".{}", ext_check);
    let mut result = String::with_capacity(content.len());
    let mut pos = 0;
    let mut updated = false;

    while pos < content.len() {
        let rel = match lower[pos..].find(&tag_lower) {
            Some(r) => pos + r,
            None => {
                result.push_str(&content[pos..]);
                break;
            }
        };
        result.push_str(&content[pos..rel]);
        let tag_end = match content[rel..].find('>') {
            Some(e) => rel + e,
            None => {
                result.push_str(&content[rel..]);
                return (result, updated);
            }
        };
        let tag_content = &content[rel..=tag_end];
        let tag_lower_slice = &lower[rel..=tag_end];

        let replaced = if let Some(am) = find_attr_value(tag_content, tag_lower_slice, attr) {
            let path = &am.value;
            if !path.to_lowercase().contains(&ext_lower)
                || path.starts_with("http")
                || path.starts_with("//")
                || path.starts_with("data:")
                || should_exclude_cdn(path, exclude_map)
            {
                false
            } else {
                let mut clean = path.clone();
                loop {
                    if clean.starts_with("./") {
                        clean = clean[2..].to_string();
                    } else if clean.starts_with("../") {
                        clean = clean[3..].to_string();
                    } else {
                        break;
                    }
                }
                // After stripping ./ or ../, skip if it's already an absolute URL
                // (e.g. "./https://cdn/..." produced by a previous buggy run).
                if clean.starts_with("http")
                    || clean.starts_with("//")
                    || clean.starts_with("data:")
                {
                    false
                } else {
                    if clean.starts_with('/') {
                        clean = clean[1..].to_string();
                    }
                    let new_path = format!("{}/{}", cdn_domain, clean);
                    if new_path != *path {
                        let cdn_type = if tag == "link" { "CSS" } else { "JS" };
                        println!("  🌍 CDN({}): {} -> {}", cdn_type, path_base(path), new_path);
                        let new_tag = format!(
                            "{}{}{}",
                            &tag_content[..am.val_start],
                            new_path,
                            &tag_content[am.val_end..]
                        );
                        result.push_str(&new_tag);
                        updated = true;
                        true
                    } else {
                        false
                    }
                }
            }
        } else {
            false
        };
        if !replaced {
            result.push_str(tag_content);
        }
        pos = tag_end + 1;
    }
    (result, updated)
}

fn should_exclude_cdn(file_path: &str, exclude_map: &HashSet<String>) -> bool {
    if exclude_map.is_empty() {
        return false;
    }
    let filename = path_base(file_path);
    let filename = filename.split('?').next().unwrap_or(&filename);
    if exclude_map.contains(filename) {
        return true;
    }
    for ef in exclude_map {
        if file_path.contains(ef.as_str()) {
            return true;
        }
    }
    false
}

fn walk_for_html(
    dir: &str,
    exclude_dirs: &HashSet<String>,
    html_files: &mut Vec<String>,
    root: &str,
) {
    let entries = match std::fs::read_dir(dir) {
        Ok(e) => e,
        Err(_) => return,
    };
    for entry in entries.flatten() {
        let ft = match entry.file_type() {
            Ok(t) => t,
            Err(_) => continue,
        };
        let name = entry.file_name().to_string_lossy().to_string();
        let path_str = entry.path().to_string_lossy().to_string();
        if ft.is_dir() {
            if exclude_dirs.contains(&name) {
                continue;
            }
            walk_for_html(&path_str, exclude_dirs, html_files, root);
        } else if name.ends_with(".html") {
            html_files.push(path_relative(root, &path_str).replace('\\', "/"));
        }
    }
}

// ---------------------------------------------------------------------------
// Tests (mirror Go test suite)
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn tmp_id() -> u64 {
        use std::sync::atomic::{AtomicU64, Ordering};
        static C: AtomicU64 = AtomicU64::new(0);
        C.fetch_add(1, Ordering::SeqCst)
    }

    #[test]
    fn test_file_exists() {
        let tmp = std::env::temp_dir().join(format!("hf_test_{}.txt", tmp_id()));
        let mut f = std::fs::File::create(&tmp).unwrap();
        f.write_all(b"test").unwrap();

        assert!(file_exists(tmp.to_str().unwrap()));
        assert!(!file_exists("non_existent_file_12345.txt"));

        let _ = std::fs::remove_file(&tmp);
    }

    #[test]
    fn test_hash_filename_functions() {
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );
        let cases = [
            ("style.css", "abcdef12", "style.abcdef12.css"),
            ("app.min.js", "12345678", "app.min.12345678.js"),
            ("image.png", "11112222", "image.11112222.png"),
        ];
        for (original, hash, hashed) in cases.iter() {
            assert_eq!(vm.add_hash_to_filename(original, hash), *hashed);
            assert_eq!(vm.remove_hash_from_filename(hashed), *original);
        }
    }

    #[test]
    fn test_calculate_file_hash() {
        let content = "test content for hashing";
        let expected = md5::hex(content.as_bytes());

        let tmp = std::env::temp_dir().join(format!("hash_test_{}.txt", tmp_id()));
        std::fs::write(&tmp, content).unwrap();

        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );
        assert_eq!(
            vm.calculate_file_hash(tmp.to_str().unwrap()).unwrap(),
            &expected[..8]
        );

        let vm_full = VersionManager::new(
            Config {
                hash_length: 0,
                ..Default::default()
            },
            false,
        );
        assert_eq!(
            vm_full.calculate_file_hash(tmp.to_str().unwrap()).unwrap(),
            expected
        );

        assert_eq!(get_file_hash(tmp.to_str().unwrap()).unwrap(), expected);

        let _ = std::fs::remove_file(&tmp);
    }

    #[test]
    fn test_should_process_component() {
        let vm = VersionManager::new(
            Config {
                include_components: vec!["button".into(), "modal".into()],
                ..Default::default()
            },
            false,
        );
        assert!(vm.should_process_component("/components/button/style.css"));
        assert!(vm.should_process_component("\\components\\modal\\script.js"));
        assert!(!vm.should_process_component("/components/footer/style.css"));
        assert!(vm.should_process_component("button.js"));
        assert!(!vm.should_process_component("header.js"));
    }

    #[test]
    fn test_should_exclude_from_cdn() {
        let vm = VersionManager::new(
            Config {
                cdn_exclude_files: vec!["global.css".into(), "jquery.js".into()],
                ..Default::default()
            },
            false,
        );
        assert!(vm.should_exclude_from_cdn("css/global.css"));
        assert!(vm.should_exclude_from_cdn("js/jquery.js"));
        assert!(vm.should_exclude_from_cdn("global.css?v=123"));
        assert!(!vm.should_exclude_from_cdn("css/main.css"));
        assert!(!vm.should_exclude_from_cdn("js/app.js"));
    }

    #[test]
    fn test_update_css_image_references() {
        let css = ".bg1 { background: url('../images/bg.png'); }\n.bg2 { background-image: url(\"img/icon.svg?v=1\"); }\n";
        let tmp = std::env::temp_dir().join(format!("style_{}.css", tmp_id()));
        std::fs::write(&tmp, css).unwrap();

        let vm = VersionManager::new(Config::default(), true);
        let mut map = HashMap::new();
        map.insert("../images/bg.png".into(), "bg.abcdef12.png".into());
        map.insert("img/icon.svg".into(), "icon.12345678.svg".into());

        vm.update_css_image_references(tmp.to_str().unwrap(), &map)
            .unwrap();

        let result = std::fs::read_to_string(&tmp).unwrap();
        assert!(result.contains("url('../images/bg.abcdef12.png')"));
        assert!(result.contains("url(\"img/icon.12345678.svg\")"));

        let _ = std::fs::remove_file(&tmp);
    }

    #[test]
    fn test_collect_images_from_css() {
        let dir = std::env::temp_dir().join(format!("css_img_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();

        let css = ".t1 { background: url('test1.png'); }\n.t2 { background: url(\"test2.jpg\"); }\n.t3 { background: url(http://x.com/t.jpg); }\n.t4 { background: url(data:image/png;base64,abc); }\n";
        let css_path = dir.join("style.css");
        std::fs::write(&css_path, css).unwrap();
        std::fs::write(dir.join("test1.png"), "data").unwrap();
        std::fs::write(dir.join("test2.jpg"), "data").unwrap();

        let vm = VersionManager::new(Config::default(), false);
        let images = vm
            .collect_images_from_css(css_path.to_str().unwrap())
            .unwrap();
        assert_eq!(images.len(), 2);

        let found1 = images.iter().any(|i| i.original_path == "test1.png");
        let found2 = images.iter().any(|i| i.original_path == "test2.jpg");
        assert!(found1 && found2);

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_copy_file() {
        let dir = std::env::temp_dir().join(format!("cp_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let src = dir.join("source.txt");
        let dst = dir.join("dest.txt");
        let content = b"file copy test content";
        std::fs::write(&src, content).unwrap();

        copy_file(src.to_str().unwrap(), dst.to_str().unwrap()).unwrap();

        let got = std::fs::read(&dst).unwrap();
        assert_eq!(got, content);

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_is_js_or_css() {
        assert!(is_js_or_css("style.css"));
        assert!(is_js_or_css("app.js"));
        assert!(is_js_or_css("STYLE.CSS"));
        assert!(is_js_or_css("APP.JS"));
        assert!(is_js_or_css("app.min.js"));
        assert!(!is_js_or_css("image.png"));
        assert!(!is_js_or_css("data.json"));
        assert!(!is_js_or_css("noext"));
    }

    #[test]
    fn test_add_hash_to_filename_edge() {
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );
        assert_eq!(
            vm.add_hash_to_filename("style.css", "abcd1234"),
            "style.abcd1234.css"
        );
        assert_eq!(
            vm.add_hash_to_filename("app.min.js", "aabbccdd"),
            "app.min.aabbccdd.js"
        );
        assert_eq!(
            vm.add_hash_to_filename("style.aaaabbbb.css", "ccccdddd"),
            "style.ccccdddd.css"
        );
        assert_eq!(
            vm.add_hash_to_filename("Makefile", "aabbccdd"),
            "Makefile.aabbccdd"
        );
    }

    #[test]
    fn test_remove_hash_from_filename_edge() {
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );
        assert_eq!(
            vm.remove_hash_from_filename("style.abcdef12.css"),
            "style.css"
        );
        assert_eq!(
            vm.remove_hash_from_filename("app.min.12345678.js"),
            "app.min.js"
        );
        assert_eq!(vm.remove_hash_from_filename("style.css"), "style.css");
        assert_eq!(
            vm.remove_hash_from_filename("style.abc.css"),
            "style.abc.css"
        );
    }

    #[test]
    fn test_find_file() {
        let dir = std::env::temp_dir().join(format!("ff_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );

        let plain = dir.join("style.css");
        std::fs::write(&plain, "css").unwrap();
        assert_eq!(
            vm.find_file(plain.to_str().unwrap()),
            plain.to_str().unwrap()
        );

        std::fs::remove_file(&plain).unwrap();
        let hashed = dir.join("style.abcd1234.css");
        std::fs::write(&hashed, "css").unwrap();
        let got = vm.find_file(plain.to_str().unwrap());
        assert!(!got.is_empty());
        assert_eq!(path_base(&got), "style.abcd1234.css");

        assert_eq!(
            vm.find_file(dir.join("nonexistent").join("style.css").to_str().unwrap()),
            ""
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_find_and_delete_old_hash_files() {
        let dir = std::env::temp_dir().join(format!("fd_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );

        std::fs::write(dir.join("style.aaaabbbb.css"), "current").unwrap();
        std::fs::write(dir.join("style.ccccdddd.css"), "old").unwrap();
        std::fs::write(dir.join("style.eeeeffff.css"), "older").unwrap();
        std::fs::write(dir.join("other.css"), "unrelated").unwrap();

        vm.find_and_delete_old_hash_files(dir.to_str().unwrap(), "style", ".css", "aaaabbbb")
            .unwrap();

        assert!(file_exists(
            dir.join("style.aaaabbbb.css").to_str().unwrap()
        ));
        assert!(!file_exists(
            dir.join("style.ccccdddd.css").to_str().unwrap()
        ));
        assert!(!file_exists(
            dir.join("style.eeeeffff.css").to_str().unwrap()
        ));
        assert!(file_exists(dir.join("other.css").to_str().unwrap()));

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_rename_file_with_hash() {
        let dir = std::env::temp_dir().join(format!("rh_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );

        let src = dir.join("app.js");
        std::fs::write(&src, "console.log(1)").unwrap();

        let info = vm.rename_file_with_hash(src.to_str().unwrap()).unwrap();

        assert!(file_exists(&info.hashed_path));
        assert!(file_exists(src.to_str().unwrap()));
        assert_eq!(info.hash.len(), 8);
        assert!(path_base(&info.hashed_path).contains(&info.hash));

       let _ = std::fs::remove_dir_all(&dir);
   }

   #[test]
    fn test_rename_file_with_hash_stages_in_git() {
        // Regression for the "unversioned files" production incident:
        // rename_file_with_hash must git-add the newly created hashed file so
        // it appears as staged (A) in git status, not as unversioned (??).
        // The old Rust binary never called git add, leaving every hashed JS/CSS
        // and image file unversioned in the source repo.
        use std::process::Command;

        if Command::new("git").arg("--version").output().is_err() {
            return;
        }

        let dir = std::env::temp_dir().join(format!("gitstage_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        Command::new("git")
            .args(["init", "--quiet"])
            .current_dir(&dir)
            .output()
            .unwrap();

        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );
        let src = dir.join("xdrNormal.js");
        std::fs::write(&src, "console.log(1)").unwrap();

        let info = vm.rename_file_with_hash(src.to_str().unwrap()).unwrap();
        let hashed_name = path_base(&info.hashed_path);
        assert!(file_exists(&info.hashed_path), "hashed file must be created");

        let out = Command::new("git")
            .args(["status", "--porcelain"])
            .current_dir(&dir)
            .output()
            .unwrap();
        let status = String::from_utf8_lossy(&out.stdout);

        let staged = status
            .lines()
            .any(|l| l.starts_with('A') && l.contains(&hashed_name));
        assert!(
            staged,
            "hashed JS file should be staged (A) in git, not unversioned.\ngot:\n{}",
            status
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_rename_file_with_hash_stages_nested_image_in_git() {
        // Regression for the gift-carousel2 production incident: a hashed
        // image created in a nested subdirectory (images/.../new/) must also
        // be git-added there, so it shows as staged rather than unversioned.
        use std::process::Command;

        if Command::new("git").arg("--version").output().is_err() {
            return;
        }

        let dir = std::env::temp_dir().join(format!("gitnested_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        Command::new("git")
            .args(["init", "--quiet"])
            .current_dir(&dir)
            .output()
            .unwrap();

        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );

        // Nested image like images/xdrNormal/202505/new/gift-carousel2.png
        let img_dir = dir.join("images/xdrNormal/202505/new");
        std::fs::create_dir_all(&img_dir).unwrap();
        let src = img_dir.join("gift-carousel2.png");
        std::fs::write(&src, "PNGBYTES").unwrap();

        let info = vm.rename_file_with_hash(src.to_str().unwrap()).unwrap();
        let hashed_name = path_base(&info.hashed_path);
        assert!(file_exists(&info.hashed_path), "nested hashed image must be created");

        // git status from the repo root shows nested staged files with their
        // repo-relative path, e.g. "A  images/.../gift-carousel2.<hash>.png".
        let out = Command::new("git")
            .args(["status", "--porcelain"])
            .current_dir(&dir)
            .output()
            .unwrap();
        let status = String::from_utf8_lossy(&out.stdout);

        let staged = status
            .lines()
            .any(|l| l.starts_with('A') && l.contains(&hashed_name));
        assert!(
            staged,
            "nested hashed image should be staged (A) in git, not unversioned.\ngot:\n{}",
            status
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_collect_resources_from_html() {
        let dir = std::env::temp_dir().join(format!("cr_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let html = "<!DOCTYPE html><html><head>\n<link rel=\"stylesheet\" href=\"css/index.css\">\n<link rel=\"stylesheet\" href=\"components/button/button.css\">\n<link rel=\"stylesheet\" href=\"https://cdn.example.com/components/modal/modal.css\">\n</head><body>\n<script src=\"js/index.js\"></script>\n<script src=\"components/button/button.js\"></script>\n<script src=\"https://cdn.example.com/components/modal/modal.js\"></script>\n</body></html>";
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, html).unwrap();

        let vm = VersionManager::new(
            Config {
                include_components: vec!["button".into(), "modal".into()],
                ..Default::default()
            },
            false,
        );

        let resources = vm
            .collect_resources_from_html(html_path.to_str().unwrap())
            .unwrap();
        let css = resources.get("css").unwrap();
        let js = resources.get("js").unwrap();
        assert!(css.iter().any(|c| c.contains("button/button.css")));
        assert!(js.iter().any(|j| j.contains("button/button.js")));

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_update_html_content() {
        let dir = std::env::temp_dir().join(format!("uh_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let html =
            "<link rel=\"stylesheet\" href=\"css/style.css\">\n<script src=\"js/app.js\"></script>";
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, html).unwrap();

        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                ..Default::default()
            },
            false,
        );
        let mut resources = HashMap::new();
        let mut css_map = HashMap::new();
        css_map.insert("css/style.css".into(), "css/style.aaaabbbb.css".into());
        resources.insert("css".into(), css_map);
        let mut js_map = HashMap::new();
        js_map.insert("js/app.js".into(), "js/app.ccccdddd.js".into());
        resources.insert("js".into(), js_map);

        vm.update_html_content(html_path.to_str().unwrap(), &resources)
            .unwrap();

        let result = std::fs::read_to_string(&html_path).unwrap();
        assert!(result.contains("style.aaaabbbb.css"));
        assert!(result.contains("app.ccccdddd.js"));

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_cdn_no_double_prefix_with_dot_slash() {
        // Regression: ./-prefixed paths must not produce "./https://..." which
        // the generic CDN pass then double-prefixes into "https://cdn/https://cdn/...".
        let dir = std::env::temp_dir().join(format!("cdn_ds_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let html = "<link rel=\"stylesheet\" href=\"./css/style.css\">\n<script src=\"./js/app.js\"></script>";
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, html).unwrap();

        let cdn = "https://cdn.example.com";
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                cdn_domain: cdn.to_string(),
                ..Default::default()
            },
            false,
        );
        let mut resources = HashMap::new();
        let mut css_map = HashMap::new();
        css_map.insert("css/style.css".into(), "css/style.aaaabbbb.css".into());
        resources.insert("css".into(), css_map);
        let mut js_map = HashMap::new();
        js_map.insert("js/app.js".into(), "js/app.ccccdddd.js".into());
        resources.insert("js".into(), js_map);

        vm.update_html_content(html_path.to_str().unwrap(), &resources)
            .unwrap();

        let result = std::fs::read_to_string(&html_path).unwrap();
        // Should have clean CDN URLs without ./ prefix
        assert!(
            result.contains("https://cdn.example.com/css/style.aaaabbbb.css"),
            "expected clean CDN URL, got: {result}"
        );
        assert!(
            result.contains("https://cdn.example.com/js/app.ccccdddd.js"),
            "expected clean CDN URL, got: {result}"
        );
        // Must NOT have "./https://" (double-prefix precursor)
        assert!(
            !result.contains("./https://"),
            "found ./https:// prefix (double-prefix bug), got: {result}"
        );
        // Must NOT have "https://cdn.example.com/https://" (full double-prefix)
        assert!(
            !result.contains("https://cdn.example.com/https://"),
            "found double CDN prefix, got: {result}"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_cdn_generic_skips_prefixed_absolute_url() {
        // Regression: a "./https://..." URL (from a previous buggy run) must
        // not be double-prefixed by the generic CDN pass.
        let dir = std::env::temp_dir().join(format!("cdn_gp_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let html = "<link rel=\"stylesheet\" href=\"./https://cdn.example.com/css/style.css\">";
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, html).unwrap();

        let cdn = "https://cdn.example.com";
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                cdn_domain: cdn.to_string(),
                ..Default::default()
            },
            false,
        );
        // Empty resource map — only apply_generic_cdn runs
        let resources: HashMap<String, HashMap<String, String>> = HashMap::new();

        vm.update_html_content(html_path.to_str().unwrap(), &resources)
            .unwrap();

        let result = std::fs::read_to_string(&html_path).unwrap();
        // Must NOT have "https://cdn.example.com/https://" (double-prefix)
        assert!(
            !result.contains("https://cdn.example.com/https://"),
            "found double CDN prefix, got: {result}"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_find_all_html_files() {
        let dir = std::env::temp_dir().join(format!("fa_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();

        std::fs::write(dir.join("index.html"), "1").unwrap();
        std::fs::create_dir_all(dir.join("sub")).unwrap();
        std::fs::write(dir.join("sub").join("page.html"), "2").unwrap();
        std::fs::create_dir_all(dir.join("node_modules")).unwrap();
        std::fs::write(dir.join("node_modules").join("lib.html"), "3").unwrap();
        std::fs::write(dir.join("readme.txt"), "4").unwrap();

        let vm = VersionManager::new(
            Config {
                root_dir: dir.to_string_lossy().to_string(),
                exclude_dirs: vec!["node_modules".into()],
                ..Default::default()
            },
            false,
        );

        let files = vm.find_all_html_files();
        assert_eq!(files.len(), 2);
        assert!(files.iter().all(|f| !f.contains("node_modules")));

        let _ = std::fs::remove_dir_all(&dir);
    }

    // ===================================================================
    // Comprehensive CDN double-prefix regression tests
    //
    // These tests guard against the bug where CDN URLs get double-prefixed
    // (e.g. https://cdn/https://cdn/components/...) which causes the
    // deploy validation to fail with "file missing".
    // ===================================================================

    #[test]
    fn test_e2e_process_html_no_double_prefix() {
        // End-to-end: create a realistic HTML with component resources,
        // process through process_html_file, verify CDN URLs have no
        // double prefix. This is the single most important regression test.
        let dir = std::env::temp_dir().join(format!("e2e_{}", tmp_id()));
        let comp_dir = dir.join("components").join("xdrsignNew");
        std::fs::create_dir_all(&comp_dir).unwrap();

        std::fs::write(comp_dir.join("index.css"), "body{color:red}").unwrap();
        std::fs::write(comp_dir.join("index.js"), "console.log(1)").unwrap();

        let html = "<!DOCTYPE html>\n\
            <html>\n<head>\n\
            <link rel=\"stylesheet\" href=\"components/xdrsignNew/index.css\">\n\
            </head>\n<body>\n\
            <script src=\"components/xdrsignNew/index.js\"></script>\n\
            </body>\n</html>";
        let html_path = dir.join("page.html");
        std::fs::write(&html_path, html).unwrap();

        let cdn = "https://cdn.example.com";
        let mut vm = VersionManager::new(
            Config {
                hash_length: 8,
                cdn_domain: cdn.to_string(),
                ..Default::default()
            },
            false,
        );

        vm.process_html_file(html_path.to_str().unwrap()).unwrap();

        let result = std::fs::read_to_string(&html_path).unwrap();

        // Must contain clean CDN URLs (cdn + /components/.../index.HASH.ext)
        assert!(
            result.contains("https://cdn.example.com/components/xdrsignNew/index.")
                && result.contains(".css"),
            "expected CDN-prefixed CSS URL, got: {result}"
        );
        assert!(
            result.contains("https://cdn.example.com/components/xdrsignNew/index.")
                && result.contains(".js"),
            "expected CDN-prefixed JS URL, got: {result}"
        );

        // Must NOT have any double-prefix patterns
        assert!(
            !result.contains("https://cdn.example.com/https://"),
            "found double CDN prefix, got: {result}"
        );
        assert!(
            !result.contains("./https://"),
            "found ./https:// precursor, got: {result}"
        );
        assert!(
            !result.contains("../https://"),
            "found ../https:// precursor, got: {result}"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_e2e_process_html_idempotent() {
        // Running process_html_file twice must not change the result.
        // This catches bugs where re-running on already-processed HTML
        // introduces double prefixes.
        let dir = std::env::temp_dir().join(format!("e2e_idem_{}", tmp_id()));
        let comp_dir = dir.join("components").join("xdrsignNew");
        std::fs::create_dir_all(&comp_dir).unwrap();

        std::fs::write(comp_dir.join("index.css"), "body{color:red}").unwrap();
        std::fs::write(comp_dir.join("index.js"), "console.log(1)").unwrap();

        let html = "<link rel=\"stylesheet\" href=\"components/xdrsignNew/index.css\">\n\
            <script src=\"components/xdrsignNew/index.js\"></script>";
        let html_path = dir.join("page.html");
        std::fs::write(&html_path, html).unwrap();

        let cdn = "https://cdn.example.com";
        let cfg = Config {
            hash_length: 8,
            cdn_domain: cdn.to_string(),
            ..Default::default()
        };

        // First run
        let mut vm1 = VersionManager::new(cfg.clone(), false);
        vm1.process_html_file(html_path.to_str().unwrap()).unwrap();
        let after_first = std::fs::read_to_string(&html_path).unwrap();

        // Second run with a fresh VM (simulates re-running the binary)
        let mut vm2 = VersionManager::new(cfg, false);
        vm2.process_html_file(html_path.to_str().unwrap()).unwrap();
        let after_second = std::fs::read_to_string(&html_path).unwrap();

        assert_eq!(
            after_first, after_second,
            "second run changed the HTML (not idempotent)"
        );
        assert!(
            !after_second.contains("https://cdn.example.com/https://"),
            "double prefix after second run"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_e2e_process_html_dot_slash_no_double_prefix() {
        // ./-prefixed component paths must not produce ./https://...
        let dir = std::env::temp_dir().join(format!("e2e_ds_{}", tmp_id()));
        let comp_dir = dir.join("components").join("xdrsignNew");
        std::fs::create_dir_all(&comp_dir).unwrap();

        std::fs::write(comp_dir.join("index.css"), "body{color:red}").unwrap();

        let html = "<link rel=\"stylesheet\" href=\"./components/xdrsignNew/index.css\">";
        let html_path = dir.join("page.html");
        std::fs::write(&html_path, html).unwrap();

        let cdn = "https://cdn.example.com";
        let mut vm = VersionManager::new(
            Config {
                hash_length: 8,
                cdn_domain: cdn.to_string(),
                ..Default::default()
            },
            false,
        );
        vm.process_html_file(html_path.to_str().unwrap()).unwrap();

        let result = std::fs::read_to_string(&html_path).unwrap();
        assert!(
            result.contains("https://cdn.example.com/components/xdrsignNew/index."),
            "expected CDN URL, got: {result}"
        );
        assert!(
            !result.contains("./https://"),
            "found ./https:// precursor, got: {result}"
        );
        assert!(
            !result.contains("https://cdn.example.com/https://"),
            "found double prefix, got: {result}"
        );

       let _ = std::fs::remove_dir_all(&dir);
   }

   #[test]
   fn test_e2e_process_html_extra_hash_resources() {
       // A shared script (e.g. utils_index.js) referenced via a relative
       // path with a ?query, configured under extraHashResources, must be
       // hashed and its HTML reference updated to a CDN URL — even though
       // the path does not contain "components".
       let dir = std::env::temp_dir().join(format!("e2e_ehr_{}", tmp_id()));
       let common_dir = dir.join("scripts").join("common");
       std::fs::create_dir_all(&common_dir).unwrap();

       std::fs::write(common_dir.join("utils_index.js"), "console.log('utils');").unwrap();

       let html = "<!DOCTYPE html>\n\
           <html>\n<head></head>\n<body>\n\
           <script type=\"text/javascript\" src=\"./scripts/common/utils_index.js?2505141\"></script>\n\
           </body>\n</html>";
       let html_path = dir.join("page.html");
       std::fs::write(&html_path, html).unwrap();

       let cdn = "https://cdn.example.com";
       let mut vm = VersionManager::new(
           Config {
               hash_length: 8,
               cdn_domain: cdn.to_string(),
               process_main_resources: vec!["page".to_string()],
               extra_hash_resources: vec!["scripts/common/utils_index.js".to_string()],
               ..Default::default()
           },
           false,
       );

       vm.process_html_file(html_path.to_str().unwrap()).unwrap();

       let result = std::fs::read_to_string(&html_path).unwrap();

       // The old ?query reference must be gone.
       assert!(
           !result.contains("utils_index.js?2505141"),
           "old ?query reference still present: {result}"
       );

       // Must contain a CDN-prefixed hashed reference.
       assert!(
           result.contains("https://cdn.example.com/scripts/common/utils_index.")
               && result.contains(".js"),
           "expected CDN-prefixed hashed utils_index URL, got: {result}"
       );

       // The hashed file must exist on disk.
       let has_hashed = std::fs::read_dir(&common_dir)
           .unwrap()
           .filter_map(|e| e.ok())
           .any(|e| {
               let n = e.file_name().to_string_lossy().to_string();
               n.starts_with("utils_index.") && n.ends_with(".js") && n != "utils_index.js"
           });
       assert!(
           has_hashed,
           "hashed utils_index.*.js not found in {}",
           common_dir.display()
       );

       let _ = std::fs::remove_dir_all(&dir);
   }

   #[test]
   fn test_apply_resource_to_tags_already_http_url() {
        // When the resource map key is already an HTTP URL (e.g. from a
        // re-run), apply_resource_to_tags must not double-prefix it.
        let html = "<link rel=\"stylesheet\" href=\"https://cdn.example.com/css/style.aaaabbbb.css\">";
        let mut map = HashMap::new();
        map.insert(
            "https://cdn.example.com/css/style.aaaabbbb.css".into(),
            "css/style.aaaabbbb.css".into(),
        );
        let exclude: HashSet<String> = HashSet::new();

        let (result, _updated) = apply_resource_to_tags(
            html,
            "link",
            "href",
            &map,
            "https://cdn.example.com",
            &exclude,
        );

        assert!(
            result.contains("https://cdn.example.com/css/style.aaaabbbb.css"),
            "expected unchanged CDN URL, got: {result}"
        );
        assert!(
            !result.contains("https://cdn.example.com/https://"),
            "found double prefix, got: {result}"
        );
    }

    #[test]
    fn test_apply_generic_cdn_protocol_relative_url() {
        // Protocol-relative URLs (//cdn.example.com/...) must be skipped.
        let html = "<link rel=\"stylesheet\" href=\"//cdn.example.com/css/style.css\">";
        let exclude: HashSet<String> = HashSet::new();

        let (result, updated) =
            apply_generic_cdn(html, "link", "href", "css", "https://cdn.example.com", &exclude);

        assert!(!updated, "should not modify protocol-relative URL");
        assert!(
            result.contains("//cdn.example.com/css/style.css"),
            "URL should be unchanged, got: {result}"
        );
    }

    #[test]
    fn test_apply_generic_cdn_data_uri() {
        // data: URIs must be skipped.
        let html =
            "<link rel=\"stylesheet\" href=\"data:text/css;base64,Ym9keXtjb2xvcjpyZWR9\">";
        let exclude: HashSet<String> = HashSet::new();

        let (result, updated) =
            apply_generic_cdn(html, "link", "href", "css", "https://cdn.example.com", &exclude);

        assert!(!updated, "should not modify data: URI");
        assert!(
            result.contains("data:text/css"),
            "data URI should be unchanged, got: {result}"
        );
    }

    #[test]
    fn test_apply_generic_cdn_already_cdn_url() {
        // URLs that already start with http must not be double-prefixed.
        let html = "<link rel=\"stylesheet\" href=\"https://cdn.example.com/css/style.css\">";
        let exclude: HashSet<String> = HashSet::new();

        let (result, updated) =
            apply_generic_cdn(html, "link", "href", "css", "https://cdn.example.com", &exclude);

        assert!(!updated, "should not modify already-CDN URL");
        assert!(
            !result.contains("https://cdn.example.com/https://"),
            "found double prefix, got: {result}"
        );
    }

    #[test]
    fn test_apply_generic_cdn_dot_dot_slash_prefix() {
        // ../-prefixed non-hash paths must get correct CDN URLs.
        let html = "<link rel=\"stylesheet\" href=\"../css/style.css\">";
        let exclude: HashSet<String> = HashSet::new();

        let (result, updated) =
            apply_generic_cdn(html, "link", "href", "css", "https://cdn.example.com", &exclude);

        assert!(updated, "should have updated ../-prefixed path");
        assert!(
            result.contains("https://cdn.example.com/css/style.css"),
            "expected CDN URL without ../, got: {result}"
        );
        assert!(
            !result.contains("../https://"),
            "found ../https:// precursor, got: {result}"
        );
    }

    #[test]
    fn test_cdn_no_double_prefix_multiple_resources() {
        // Multiple resources with different prefixes in the same HTML.
        let html = "<link rel=\"stylesheet\" href=\"components/a/style.css\">\n\
            <link rel=\"stylesheet\" href=\"./components/b/style.css\">\n\
            <script src=\"components/c/app.js\"></script>\n\
            <script src=\"./components/d/app.js\"></script>";
        let dir = std::env::temp_dir().join(format!("cdn_multi_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, html).unwrap();

        let cdn = "https://cdn.example.com";
        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                cdn_domain: cdn.to_string(),
                ..Default::default()
            },
            false,
        );

        // Empty resource maps: only apply_generic_cdn runs
        let resources: HashMap<String, HashMap<String, String>> = HashMap::new();
        vm.update_html_content(html_path.to_str().unwrap(), &resources)
            .unwrap();

        let result = std::fs::read_to_string(&html_path).unwrap();

        assert!(
            result.contains("https://cdn.example.com/components/a/style.css"),
            "missing CDN URL for components/a, got: {result}"
        );
        assert!(
            result.contains("https://cdn.example.com/components/b/style.css"),
            "missing CDN URL for ./components/b, got: {result}"
        );
        assert!(
            result.contains("https://cdn.example.com/components/c/app.js"),
            "missing CDN URL for components/c, got: {result}"
        );
        assert!(
            result.contains("https://cdn.example.com/components/d/app.js"),
            "missing CDN URL for ./components/d, got: {result}"
        );
        assert!(
            !result.contains("https://cdn.example.com/https://"),
            "found double prefix, got: {result}"
        );
        assert!(
            !result.contains("./https://"),
            "found ./https:// precursor, got: {result}"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_cdn_domain_with_path_prefix_no_double_prefix() {
        // CDN domain that includes a path prefix (like the real config:
        // https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap) must not
        // cause double-prefixing.
        let cdn = "https://qqt-res.cmicrwx.cn/2016tyjf/xhmqqthy/res/wap";
        let html = "<link rel=\"stylesheet\" href=\"components/xdrsignNew/index.css\">\n\
            <script src=\"components/xdrsignNew/index.js\"></script>";
        let dir = std::env::temp_dir().join(format!("cdn_pp_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, html).unwrap();

        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                cdn_domain: cdn.to_string(),
                ..Default::default()
            },
            false,
        );

        let resources: HashMap<String, HashMap<String, String>> = HashMap::new();
        vm.update_html_content(html_path.to_str().unwrap(), &resources)
            .unwrap();

        let result = std::fs::read_to_string(&html_path).unwrap();

        assert!(
            result.contains(&format!("{}/components/xdrsignNew/index.css", cdn)),
            "expected CDN URL with path prefix, got: {result}"
        );
        assert!(
            result.contains(&format!("{}/components/xdrsignNew/index.js", cdn)),
            "expected CDN URL with path prefix, got: {result}"
        );
        // The CDN domain appears exactly once per URL (no double prefix)
        let count = result.matches(cdn).count();
        assert_eq!(
            count, 2,
            "expected CDN domain to appear exactly 2 times (once per resource), got {count}: {result}"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_update_comment_dates() {
        let html = "<!--\n@create date 2025-04-18 17:05:01\n@modify date 2025-04-18 17:05:01\n-->\n<div>hello</div>";
        let (result, updated) = update_comment_dates(html);
        assert!(updated, "dates should be updated");
        assert!(
            !result.contains("2025-04-18 17:05:01"),
            "old date should be gone"
        );
        assert!(result.contains("@create date"), "keyword preserved");
        assert!(result.contains("@modify date"), "keyword preserved");
        // New date should be 19 chars matching YYYY-MM-DD HH:MM:SS
        let create_idx = result.find("@create date ").unwrap() + "@create date ".len();
        let new_date = &result[create_idx..create_idx + 19];
        assert!(
            is_date_time_format(new_date),
            "new date should be valid: {new_date}"
        );
    }

    #[test]
    fn test_format_now() {
        let now = format_now();
        assert_eq!(now.len(), 19, "should be 19 chars: {now}");
        assert!(is_date_time_format(&now), "should be valid date-time: {now}");
    }

    #[test]
    fn test_obfuscate_js_strips_comments_and_mangles() {
        let input = b"// This is a comment\nvar greetingMessage = function() {\n    var descriptiveLocalVariable = \"hello world\";\n    console.log(descriptiveLocalVariable);\n};\ngreetingMessage();\n";
        let output = obfuscate_js(input);
        let s = String::from_utf8_lossy(&output);

        // Comments must be stripped
        assert!(!s.contains("This is a comment"), "comment not stripped: {s}");
        // Local variable should be mangled
        assert!(!s.contains("descriptiveLocalVariable"), "local var not mangled: {s}");
        // Output should be smaller
        assert!(output.len() < input.len(), "output ({}) should be smaller than input ({})", output.len(), input.len());
    }

    #[test]
    fn test_hash_from_bytes() {
        // MD5 of empty string
        let h = hash_from_bytes(b"", 8);
        assert_eq!(h, "d41d8cd9");
        // Full hash when hash_length=0
        let full = hash_from_bytes(b"", 0);
        assert_eq!(full, "d41d8cd98f00b204e9800998ecf8427e");
    }

    #[test]
    fn test_rename_file_with_hash_obfuscate() {
        let dir = std::env::temp_dir().join(format!("hashcdn_obf_test_{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let common = dir.join("scripts/common");
        std::fs::create_dir_all(&common).unwrap();

        let js_content = b"// This is a comment\nvar greetingMessage = function() {\n    var descriptiveLocalVariable = \"hello world\";\n    console.log(descriptiveLocalVariable);\n};\ngreetingMessage();\n";
        let js_path = common.join("loginxdrNew.js");
        std::fs::write(&js_path, js_content).unwrap();

        let vm = VersionManager::new(
            Config {
                hash_length: 8,
                obfuscate_js: true,
                ..Default::default()
            },
            false,
        );

        let info = vm.rename_file_with_hash(js_path.to_str().unwrap()).unwrap();

        // Hashed file should exist
        assert!(std::path::Path::new(&info.hashed_path).exists(), "hashed file not found: {}", info.hashed_path);

        let result = std::fs::read(&info.hashed_path).unwrap();
        let s = String::from_utf8_lossy(&result);

        // Comments stripped
        assert!(!s.contains("This is a comment"), "comment not stripped");
        // Local var mangled
        assert!(!s.contains("descriptiveLocalVariable"), "local var not mangled");
        // Smaller than original
        assert!(result.len() < js_content.len(), "obfuscated JS should be smaller");

        let _ = std::fs::remove_dir_all(&dir);
    }
}
