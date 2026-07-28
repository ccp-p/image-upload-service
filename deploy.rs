//! Deploy functionality: file copying with hash versions, CDN resource validation.
//! Mirrors the Go DeployManager in cmd/hashCdn/main.go.
//! Uses a persistent on-disk cache to avoid recomputing MD5 on unchanged files.

use std::collections::HashMap;
use std::path::{Path, PathBuf};

use crate::config::{is_home_env, DeployConfig};
use crate::json::JsonValue;
use crate::patterns::{matches_alnum_hash, remove_html_comments};
use crate::version_manager::{copy_file, file_exists, get_file_hash, is_js_or_css};

// ---------------------------------------------------------------------------
// Path helpers (local copies; version_manager has its own private set)
// ---------------------------------------------------------------------------

fn path_base(path: &str) -> String {
    match path.rfind(|c: char| c == '/' || c == '\\') {
        Some(p) => path[p + 1..].to_string(),
        None => path.to_string(),
    }
}

fn path_dir(path: &str) -> String {
    match path.rfind(|c: char| c == '/' || c == '\\') {
        Some(0) => ".".to_string(),
        Some(p) => path[..p].to_string(),
        None => ".".to_string(),
    }
}

fn path_join(dir: &str, name: &str) -> String {
    // Strip leading separators to match Go's filepath.Join behaviour.
    // Without this, PathBuf::join treats "/foo" as root-relative on Windows,
    // producing "D:\foo" instead of "dir\foo".
    let name = name.trim_start_matches(|c| c == '/' || c == '\\');
    PathBuf::from(dir).join(name).to_string_lossy().to_string()
}

/// Recursively collects every regular file under `root`, recording each path
/// relative to `base` using forward slashes. Directories are descended into.
fn walk_dir_collect(root: &Path, base: &Path, out: &mut Vec<String>) -> Result<(), String> {
    for entry in std::fs::read_dir(root).map_err(|e| e.to_string())?.flatten() {
        let ft = match entry.file_type() {
            Ok(ft) => ft,
            Err(_) => continue,
        };
        if ft.is_dir() {
            walk_dir_collect(&entry.path(), base, out)?;
        } else if ft.is_file() {
            let rel = entry
                .path()
                .strip_prefix(base)
                .map_err(|e| e.to_string())?
                .to_string_lossy()
                .replace('\\', "/");
            out.push(rel);
        }
    }
    Ok(())
}

fn get_ext(filename: &str) -> String {
    match filename.rfind('.') {
        Some(pos) => filename[pos..].to_string(),
        None => String::new(),
    }
}

fn mod_time_nanos(metadata: &std::fs::Metadata) -> i64 {
    metadata
        .modified()
        .ok()
        .and_then(|t| t.duration_since(std::time::UNIX_EPOCH).ok())
        .map(|d| d.as_nanos() as i64)
        .unwrap_or(0)
}

/// Normalises a path for use as a cache key (forward slashes, strips leading ./).
fn clean_key(p: &str) -> String {
    let p = p.replace('\\', "/");
    let trimmed = p.trim_start_matches("./");
    trimmed.to_string()
}

// ---------------------------------------------------------------------------
// FileCacheEntry / DeployCache
// ---------------------------------------------------------------------------

#[derive(Debug, Clone)]
pub struct FileCacheEntry {
    pub hash: String,
    pub size: i64,
    pub mod_time: i64,
}

pub struct DeployCache {
    pub files: HashMap<String, FileCacheEntry>,
    cache_path: String,
    dirty: bool,
}

/// Loads the deploy cache from disk. Returns an empty cache if the file is
/// missing or unparseable. modTime is read as an exact i64 (Integer variant)
/// so nanosecond precision is preserved; the legacy string form written by
/// older Rust builds is still accepted as a fallback.
pub fn load_deploy_cache(cache_path: &str) -> DeployCache {
    let mut cache = DeployCache {
        files: HashMap::new(),
        cache_path: cache_path.to_string(),
        dirty: false,
    };

    let content = match std::fs::read_to_string(cache_path) {
        Ok(c) => c,
        Err(_) => return cache,
    };

    let json = match JsonValue::parse(&content) {
        Ok(j) => j,
        Err(_) => return cache,
    };

    if let JsonValue::Object(entries) = &json {
        for (key, val) in entries {
           let hash = val.get_str("hash").unwrap_or("").to_string();
           let size = val.get_num("size").map(|n| n as i64).unwrap_or(0);
            // modTime: prefer exact integer (Go numeric form), fall back to
            // legacy Rust string form for older cache files.
            let mod_time = val
                .get_i64("modTime")
                .or_else(|| val.get_str("modTime").and_then(|s| s.parse::<i64>().ok()))
                .unwrap_or(0);
            cache.files.insert(
                key.clone(),
                FileCacheEntry {
                    hash,
                    size,
                    mod_time,
                },
            );
        }
    }

    cache
}

/// Serialises the cache to pretty JSON. modTime is stored as a JSON integer
/// (not a string) so that Go's `encoding/json` can unmarshal it into int64,
/// and i64 precision is preserved exactly via the Integer variant.
fn serialize_cache(files: &HashMap<String, FileCacheEntry>) -> String {
    let mut entries: Vec<(String, JsonValue)> = Vec::new();
    for (k, v) in files {
        let entry = JsonValue::Object(vec![
            ("hash".to_string(), JsonValue::String(v.hash.clone())),
            ("size".to_string(), JsonValue::Number(v.size as f64)),
            ("modTime".to_string(), JsonValue::Integer(v.mod_time)),
        ]);
        entries.push((k.clone(), entry));
    }
    JsonValue::Object(entries).to_json_pretty()
}

impl DeployCache {
    pub fn save(&mut self) -> Result<(), String> {
        if !self.dirty {
            return Ok(());
        }
        let data = serialize_cache(&self.files);
        std::fs::write(&self.cache_path, data.as_bytes()).map_err(|e| e.to_string())?;
        self.dirty = false;
        Ok(())
    }

    /// Returns the file hash, reusing the cached value when size+modTime are
    /// unchanged; otherwise recomputes via MD5 and updates the cache.
    pub fn get_cached_hash(&mut self, file_path: &str) -> Result<String, String> {
        let key = clean_key(file_path);
        let metadata = std::fs::metadata(file_path).map_err(|e| e.to_string())?;
        let size = metadata.len() as i64;
        let mod_time = mod_time_nanos(&metadata);

        if let Some(entry) = self.files.get(&key) {
            if entry.size == size && entry.mod_time == mod_time {
                return Ok(entry.hash.clone());
            }
        }

        let hash = get_file_hash(file_path)?;
        self.files.insert(
            key,
            FileCacheEntry {
                hash: hash.clone(),
                size,
                mod_time,
            },
        );
        self.dirty = true;
        Ok(hash)
    }

    /// Writes a cache entry directly (used after copying to sync the dest hash).
    pub fn update_cache(&mut self, file_path: &str, hash: &str, size: i64, mod_time: i64) {
        let key = clean_key(file_path);
        self.files.insert(
            key,
            FileCacheEntry {
                hash: hash.to_string(),
                size,
                mod_time,
            },
        );
        self.dirty = true;
    }
}

// ---------------------------------------------------------------------------
// FileVersion / DeployManager
// ---------------------------------------------------------------------------

#[derive(Debug, Clone)]
pub struct FileVersion {
    pub path: String,
    pub name: String,
    pub has_hash: bool,
    pub mod_time: i64,
    pub hash: String,
}

pub struct DeployManager {
    pub config: DeployConfig,
    pub source_path: String,
    pub dest_path: String,
    pub debug_mode: bool,
    pub folder_opened: bool,
    pub cache: DeployCache,
}

impl DeployManager {
    pub fn new(config: DeployConfig, debug_mode: bool) -> Self {
        let is_home = is_home_env();
        let (source_path, dest_path) = if is_home {
            (
                config.home_source_path.clone(),
                config.home_dest_path.clone(),
            )
        } else {
            (
                config.company_source_path.clone(),
                config.company_dest_path.clone(),
            )
        };

        DeployManager {
            config,
            source_path,
            dest_path,
            debug_mode,
            folder_opened: false,
            cache: load_deploy_cache(".deploy-cache.json"),
        }
    }

    /// Finds all versions of a file (base + hashed), sorted by modtime desc.
    pub fn find_all_file_versions(&mut self, config_path: &str) -> Vec<FileVersion> {
        let full_path = path_join(&self.source_path, config_path);
        let dir = path_dir(&full_path);
        let file_name = path_base(&full_path);
        let ext = get_ext(&file_name);
        let basename = if !ext.is_empty() && file_name.ends_with(&ext) {
            file_name[..file_name.len() - ext.len()].to_string()
        } else {
            file_name.clone()
        };
        let ext_no_dot = ext.trim_start_matches('.');

        let mut versions = Vec::new();

        if !file_exists(&dir) {
            return versions;
        }

        // Base (no-hash) version
        if file_exists(&full_path) {
            let hash = self.cache.get_cached_hash(&full_path).unwrap_or_default();
            let mod_time = std::fs::metadata(&full_path)
                .map(|m| mod_time_nanos(&m))
                .unwrap_or(0);
            versions.push(FileVersion {
                path: full_path.clone(),
                name: file_name.clone(),
                has_hash: false,
                mod_time,
                hash,
            });
        }

        // Hashed versions: basename.[a-zA-Z0-9]+.ext
        if let Ok(entries) = std::fs::read_dir(&dir) {
            for entry in entries.flatten() {
                let name = entry.file_name().to_string_lossy().to_string();
                if name == file_name {
                    continue;
                }
                if matches_alnum_hash(&name, &basename, ext_no_dot) {
                    let file_path = path_join(&dir, &name);
                    let hash = self.cache.get_cached_hash(&file_path).unwrap_or_default();
                    let mod_time = entry.metadata().map(|m| mod_time_nanos(&m)).unwrap_or(0);
                    versions.push(FileVersion {
                        path: file_path,
                        name,
                        has_hash: true,
                        mod_time,
                        hash,
                    });
                }
            }
        }

        // Sort by modtime descending (newest first)
        versions.sort_by(|a, b| b.mod_time.cmp(&a.mod_time));
        versions
    }

    /// Removes old hashed files in the dest dir, keeping the base and the
    /// specified keep file. Returns the number of files deleted.
    pub fn clean_hash_files(&self, dest_path: &str, keep_file_name: &str) -> usize {
        let dest_dir = path_dir(dest_path);
        let dest_file_name = path_base(dest_path);
        let ext = get_ext(&dest_file_name);
        let basename = if !ext.is_empty() && dest_file_name.ends_with(&ext) {
            dest_file_name[..dest_file_name.len() - ext.len()].to_string()
        } else {
            dest_file_name.clone()
        };
        let ext_no_dot = ext.trim_start_matches('.');

        if !file_exists(&dest_dir) {
            return 0;
        }

        let mut deleted = 0;
        if let Ok(entries) = std::fs::read_dir(&dest_dir) {
            for entry in entries.flatten() {
                let name = entry.file_name().to_string_lossy().to_string();
                if name == dest_file_name || name == keep_file_name {
                    continue;
                }
                if matches_alnum_hash(&name, &basename, ext_no_dot) {
                    let file_path = path_join(&dest_dir, &name);
                    if std::fs::remove_file(&file_path).is_ok() {
                        deleted += 1;
                    }
                }
            }
        }
        deleted
    }

    /// Copies the base file and the latest hashed version from source to dest.
    /// Skips files whose content already matches (same size + hash). Cleans
    /// old hashed files in dest first. Returns (copied, skipped).
    pub fn copy_file_with_versions(
        &mut self,
        source_path: &str,
        dest_path: &str,
    ) -> Result<(usize, usize), String> {
        let versions = self.find_all_file_versions(source_path);
        if versions.is_empty() {
            return Err(format!("source file not found: {}", source_path));
        }

        // Keep only the base file and the newest hashed version
        let mut base_version: Option<FileVersion> = None;
        let mut latest_hash_version: Option<FileVersion> = None;
        for v in &versions {
            if !v.has_hash {
                base_version = Some(v.clone());
            } else if latest_hash_version.is_none() {
                latest_hash_version = Some(v.clone());
            }
        }

        let mut versions_to_process = Vec::new();
        if let Some(bv) = base_version {
            versions_to_process.push(bv);
        }
        if let Some(lv) = latest_hash_version.clone() {
            versions_to_process.push(lv);
        }

        let dest_dir = path_dir(dest_path);
        std::fs::create_dir_all(&dest_dir).map_err(|e| e.to_string())?;

        // Clean old hash files in dest, keeping the latest hash name
        if let Some(lv) = &latest_hash_version {
            self.clean_hash_files(dest_path, &lv.name);
        }

        let mut copied = 0;
        let mut skipped = 0;

        for version in &versions_to_process {
            let version_dest_path = if version.has_hash {
                path_join(&dest_dir, &version.name)
            } else {
                dest_path.to_string()
            };

            // Skip if dest already has identical content (same size + hash)
            if file_exists(&version_dest_path) {
                let src_size = std::fs::metadata(&version.path)
                    .map(|m| m.len())
                    .unwrap_or(0);
                let dst_size = std::fs::metadata(&version_dest_path)
                    .map(|m| m.len())
                    .unwrap_or(0);
                if src_size == dst_size {
                    let dest_hash = self
                        .cache
                        .get_cached_hash(&version_dest_path)
                        .unwrap_or_default();
                    if !dest_hash.is_empty() && dest_hash == version.hash {
                        skipped += 1;
                        continue;
                    }
                }
            }

            copy_file(&version.path, &version_dest_path)?;
            copied += 1;

            // Sync dest cache from source hash to avoid recomputing MD5 next time
            if let Ok(dst_meta) = std::fs::metadata(&version_dest_path) {
                let size = dst_meta.len() as i64;
                let mod_time = mod_time_nanos(&dst_meta);
                self.cache
                    .update_cache(&version_dest_path, &version.hash, size, mod_time);
            }

            if is_js_or_css(source_path) {
                println!("  copied: {} -> {}", version.name, dest_dir);
            }
        }

        Ok((copied, skipped))
    }

    /// Handles a wildcard path like "/images/*" by walking the source
    /// directory tree recursively and copying every file found (base + hash
    /// versions). Mirrors Go's DeployManager.handleWildcardPath, which uses
    /// filepath.WalkDir and therefore descends into subdirectories.
    ///
    /// The previous implementation used a single non-recursive read_dir that
    /// only listed immediate children, so files nested one level deeper
    /// (e.g. images/.../new/gift-carousel2.<hash>.png referenced from CSS)
    /// were silently skipped during deploy.
    pub fn handle_wildcard_path(
        &mut self,
        wildcard_path: &str,
    ) -> Result<(usize, usize, usize), String> {
        let dir_rel = wildcard_path.trim_end_matches("/*");
        let src_dir = path_join(&self.source_path, dir_rel);
        if !file_exists(&src_dir) {
            return Err(format!("源目录不存在: {}", src_dir));
        }

        // Collect every file under src_dir, expressed relative to src_dir with
        // forward slashes (matching Go's filepath.Rel output).
        let mut files: Vec<String> = Vec::new();
        walk_dir_collect(Path::new(&src_dir), Path::new(&src_dir), &mut files)?;

        let mut copied = 0;
        let mut skipped = 0;
        let mut failed = 0;

        for rel_sub in &files {
            // rel_to_source keeps the leading-slash form (e.g.
            // "/images/xdrNormal/202505/new/gift-carousel2.<hash>.png") so
            // path_join strips it consistently for both source and dest.
            let rel_to_source = format!("{}/{}", dir_rel, rel_sub);
            let dest = path_join(&self.dest_path, &rel_to_source);
            match self.copy_file_with_versions(&rel_to_source, &dest) {
                Ok((c, s)) => {
                    copied += c;
                    skipped += s;
                }
                Err(e) => {
                    println!("⚠️  处理失败: {} - {}", rel_to_source, e);
                    failed += 1;
                }
            }
        }

        Ok((copied, skipped, failed))
    }

    /// Validates that every CDN URL referenced in an HTML file exists in the
    /// dest directory. Returns Err with the first missing file path.
    pub fn validate_cdn_resources(&self, html_path: &str, cdn_domain: &str) -> Result<(), String> {
        if cdn_domain.is_empty() {
            return Ok(());
        }

        let content = std::fs::read_to_string(html_path).map_err(|e| e.to_string())?;
        let clean_content = remove_html_comments(&content);

        let bytes = clean_content.as_bytes();
        let mut pos = 0;

        while pos < bytes.len() {
            let remaining = &clean_content[pos..];
            let idx = match remaining.find(cdn_domain) {
                Some(i) => pos + i,
                None => break,
            };

            let path_start = idx + cdn_domain.len();
            if path_start >= bytes.len() || bytes[path_start] != b'/' {
                pos = idx + 1;
                continue;
            }

            // Capture path until whitespace, quote, or backtick
            let mut end = path_start;
            while end < bytes.len() {
                let c = bytes[end];
                if c == b' '
                    || c == b'\t'
                    || c == b'\n'
                    || c == b'\r'
                    || c == b'\''
                    || c == b'"'
                    || c == 0x60
                {
                    break;
                }
                end += 1;
            }

            if end == path_start {
                pos = path_start;
                continue;
            }

            let url_path = &clean_content[path_start..end];
            // Strip query parameters
            let url_path = url_path.split('?').next().unwrap_or(url_path);

            // Strip configured CDN path prefix to get the dest-relative path
            let rel_path = if !self.config.cdn_path_prefix.is_empty()
                && url_path.starts_with(&self.config.cdn_path_prefix)
            {
                &url_path[self.config.cdn_path_prefix.len()..]
            } else {
                url_path
            };

            let rel = rel_path.trim_start_matches('/');
            let check_path = PathBuf::from(&self.dest_path)
                .join(rel)
                .to_string_lossy()
                .to_string();

            if !file_exists(&check_path) {
                return Err(format!("file missing: {} -> {}", url_path, check_path));
            }

            pos = end;
        }

        Ok(())
    }
    /// Runs the full deploy workflow: copy files, validate CDN, save cache.
    /// Runs the full deploy workflow: svn update, copy files, validate CDN,
    /// svn add, and optionally svn commit. Mirrors Go's DeployManager.Run.
    pub fn run(
        &mut self,
        auto_commit: bool,
        commit_message: &str,
        single_html_file: &str,
        cdn_domain: &str,
    ) -> Result<(), String> {
        println!("🚀 开始部署操作...");
        println!("📂 源路径: {}", self.source_path);
        println!("📂 目标路径: {}", self.dest_path);
        println!();
        println!("💾 已加载文件缓存: {} 个条目", self.cache.files.len());
        println!();
        println!("📦 开始复制文件...");

        // Update SVN repo first (mirrors Go's updateSvnRepo).
        if is_svn_repo(&self.dest_path) {
            if let Err(e) = self.update_svn_repo() {
                println!("⚠️  SVN更新失败: {}，继续部署...", e);
            }
        }

        let file_paths = self.config.file_paths.clone();
        let mut total_copied = 0;
        let mut total_skipped = 0;
        let mut total_failed = 0;

        for file_path in &file_paths {
            if file_path.ends_with("/*") {
                match self.handle_wildcard_path(file_path) {
                    Ok((c, s, f)) => {
                        total_copied += c;
                        total_skipped += s;
                        total_failed += f;
                    }
                    Err(e) => {
                        println!("⚠️  {}", e);
                        total_failed += 1;
                    }
                }
            } else {
                let dest = path_join(&self.dest_path, file_path);
                match self.copy_file_with_versions(file_path, &dest) {
                    Ok((c, s)) => {
                        total_copied += c;
                        total_skipped += s;
                    }
                    Err(e) => {
                        println!("⚠️  处理失败: {} - {}", file_path, e);
                        total_failed += 1;
                    }
                }
            }
        }

        // Save hash cache to disk (non-fatal, mirrors Go's dm.cache.Save()).
        if let Err(e) = self.cache.save() {
            println!("⚠️  缓存保存失败: {}", e);
        }

        // Print copy summary.
        println!();
        println!("{}", "=".repeat(50));
        println!(
            "📊 复制完成: 复制 {}, 跳过 {}, 失败 {}",
            total_copied, total_skipped, total_failed
        );

        // Validate CDN resources exist in dest (non-fatal so rollback still runs).
        let mut validation_ok = true;
        if !cdn_domain.is_empty() && !single_html_file.is_empty() {
            if file_exists(single_html_file) {
                println!("🔍 正在校验 HTML 中的 CDN 资源 (已忽略注释内容)...");
                match self.validate_cdn_resources(single_html_file, cdn_domain) {
                    Ok(()) => println!("✅ 所有非注释 CDN 资源均已在目标目录就绪"),
                    Err(e) => {
                        println!("❌ CDN 资源校验失败: {}", e);
                        validation_ok = false;
                    }
                }
            }
        }

        if total_failed == 0 && validation_ok {
            println!("✅ 全部成功！");
        }
        println!("{}\n", "=".repeat(50));

        // SVN add + commit (mirrors Go's svnAddAll / svnCommit).
        if auto_commit && is_svn_repo(&self.dest_path) {
            let svn_message = if !commit_message.is_empty() {
                println!();
                println!("📝 使用自定义提交信息: {}", commit_message);
                commit_message.to_string()
            } else {
                match self.get_latest_git_commit() {
                    Ok((hash, message)) => {
                        println!();
                        println!("📝 Git提交: {} - {}", hash, message);
                        message
                    }
                    Err(e) => {
                        println!("⚠️  获取Git提交信息失败: {}", e);
                        println!("💡 请手动提交SVN修改");
                        return Ok(());
                    }
                }
            };

            println!("⏳ 2秒后开始提交...");
            std::thread::sleep(std::time::Duration::from_secs(2));

            if let Err(e) = self.svn_commit(&svn_message) {
                println!("❌ 自动提交失败: {}", e);
            } else {
                println!("🎉 自动提交完成！");
            }
        }

        Ok(())
    }

    /// Updates the SVN working copy at dest_path (mirrors Go's updateSvnRepo).
    /// If the repo is locked, runs `svn cleanup` and retries once.
    pub fn update_svn_repo(&self) -> Result<(), String> {
        println!("🔄 正在更新SVN仓库: {}", self.dest_path);

        let output = std::process::Command::new("svn")
            .arg("update")
            .current_dir(&self.dest_path)
            .output()
            .map_err(|e| format!("svn update failed: {}", e))?;

        if !output.status.success() {
            let combined = format!(
                "{}{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
            if combined.contains("locked") || combined.contains("cleanup") {
                println!("🔧 检测到SVN锁定，尝试清理...");
                let clean = std::process::Command::new("svn")
                    .arg("cleanup")
                    .current_dir(&self.dest_path)
                    .status();
                if clean.map(|s| s.success()).unwrap_or(false) {
                    return self.update_svn_repo();
                }
            }
            return Err(format!("svn update failed: {}", combined.trim()));
        }

        let stdout = String::from_utf8_lossy(&output.stdout);
        println!("✅ SVN更新成功");
        println!("{}", stdout.trim());
        Ok(())
    }

    /// Adds all unversioned (?) files to SVN (mirrors Go's svnAddAll).
    pub fn svn_add_all(&self) {
        println!("📁 正在添加新文件到SVN...");

        let output = match std::process::Command::new("svn")
            .arg("status")
            .current_dir(&self.dest_path)
            .output()
        {
            Ok(o) => o,
            Err(_) => return,
        };

        let status_text = String::from_utf8_lossy(&output.stdout).to_string();
        let mut added_count = 0;

        for line in status_text.lines() {
            let line = line.trim();
            if !line.starts_with('?') {
                continue;
            }
            let file = line[1..].trim();
            if file.is_empty() {
                continue;
            }

            let ok = std::process::Command::new("svn")
                .arg("add")
                .arg(file)
                .current_dir(&self.dest_path)
                .status()
                .map(|s| s.success())
                .unwrap_or(false);
            if ok {
                added_count += 1;
            }
        }

        if added_count > 0 {
            println!("✅ 已添加 {} 个新文件", added_count);
        }
    }

    /// Commits SVN changes (mirrors Go's svnCommit). Calls svn_add_all first.
    pub fn svn_commit(&self, message: &str) -> Result<(), String> {
        println!("📤 正在提交SVN更改...");
        println!("   提交信息: {}", message);

        // Add all new files first.
        self.svn_add_all();

        // Write commit message to a temp file with UTF-8 BOM (like Go).
        let temp_file = path_join(&self.dest_path, ".svn_commit_msg.tmp");
        let content = format!("\u{FEFF}{}", message);
        std::fs::write(&temp_file, content.as_bytes()).map_err(|e| e.to_string())?;

        let output = std::process::Command::new("svn")
            .args(["commit", "--file", &temp_file, "--encoding", "UTF-8"])
            .current_dir(&self.dest_path)
            .output()
            .map_err(|e| format!("svn commit failed: {}", e))?;

        // Clean up temp file.
        let _ = std::fs::remove_file(&temp_file);

        if !output.status.success() {
            let combined = format!(
                "{}{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
            if combined.contains("no changes") || combined.contains("没有修改") {
                println!("ℹ️  没有需要提交的修改");
                return Ok(());
            }
            return Err(format!("SVN提交失败: {}", combined.trim()));
        }

        let stdout = String::from_utf8_lossy(&output.stdout);
        println!("✅ SVN提交成功");
        println!("{}", stdout.trim());
        Ok(())
    }

    /// Gets the latest Git commit hash and message from the source repo,
    /// filtering by configured authors (mirrors Go's getLatestGitCommit).
    pub fn get_latest_git_commit(&self) -> Result<(String, String), String> {
        if !is_git_repo(&self.source_path) {
            return Err("源路径不是Git仓库".to_string());
        }

        let authors = if self.config.git_authors.is_empty() {
            vec!["chenchengpeng".to_string(), "ccp".to_string()]
        } else {
            self.config.git_authors.clone()
        };

        let mut args: Vec<String> = vec![
            "log".to_string(),
            "-1".to_string(),
            "--pretty=format:%h|%s".to_string(),
        ];
        for author in &authors {
            args.push(format!("--author={}", author));
        }

        let arg_refs: Vec<&str> = args.iter().map(|s| s.as_str()).collect();

        let output = std::process::Command::new("git")
            .args(&arg_refs)
            .current_dir(&self.source_path)
            .output()
            .map_err(|e| format!("git log failed: {}", e))?;

        let text = String::from_utf8_lossy(&output.stdout).to_string();
        let parts: Vec<&str> = text.splitn(2, '|').collect();
        if parts.len() != 2 {
            return Err("无法解析Git提交信息".to_string());
        }

        Ok((parts[0].trim().to_string(), parts[1].trim().to_string()))
    }

    /// Reverts all local changes in the dest SVN working copy (mirrors Go's
    /// revertAllSvn). Runs svn cleanup, prints status, then svn revert -R .
    pub fn revert_all_svn(&self) -> Result<(), String> {
        println!();
        println!("{}", "=".repeat(60));
        println!("🔄 回退dest SVN的所有本地变更");
        println!("{}", "=".repeat(60));
        println!("📂 目标路径: {}", self.dest_path);

        if self.dest_path.is_empty() {
            return Err(
                "未设置dest路径（请检查 version.config.json 中的 deploy.homeDestPath / deploy.companyDestPath）"
                    .to_string(),
            );
        }

        if !is_svn_repo(&self.dest_path) {
            return Err(format!("目标路径不是SVN仓库: {}", self.dest_path));
        }

        // 1. svn cleanup (handle possible interrupted/locked state)
        let clean = std::process::Command::new("svn")
            .arg("cleanup")
            .current_dir(&self.dest_path)
            .output();
        match clean {
            Ok(o) if o.status.success() => println!("✅ SVN cleanup 完成"),
            Ok(o) => {
                let combined = format!(
                    "{}{}",
                    String::from_utf8_lossy(&o.stdout),
                    String::from_utf8_lossy(&o.stderr)
                );
                println!("⚠️  SVN cleanup 失败（可忽略）: {}", combined.trim());
            }
            Err(e) => println!("⚠️  SVN cleanup 失败（可忽略）: {}", e),
        }

        // 2. Show current pending changes before reverting
        let status = std::process::Command::new("svn")
            .arg("status")
            .current_dir(&self.dest_path)
            .output();
        if let Ok(o) = status {
            let status_str = String::from_utf8_lossy(&o.stdout).trim().to_string();
            if status_str.is_empty() {
                println!("ℹ️  当前没有待提交的本地变更，无需回退");
                println!("{}", "=".repeat(60));
                return Ok(());
            }
            println!("📋 待回退的本地变更:");
            println!("{}", status_str);
        }

        // 3. Recursive revert: restore modified files, undo add/delete marks
        println!();
        println!("⏳ 正在执行 svn revert -R . ...");
        let revert = std::process::Command::new("svn")
            .args(["revert", "-R", "."])
            .current_dir(&self.dest_path)
            .status()
            .map_err(|e| format!("SVN回退失败: {}", e))?;

        if !revert.success() {
            return Err("SVN回退失败".to_string());
        }

        // 4. Remove unversioned files. `svn revert -R .` only restores
        //    versioned items; build artifacts deployed to dest survive
        //    unless explicitly deleted, leaving the workspace dirty.
        match clean_svn_unversioned(&self.dest_path) {
            Ok((0, 0)) => println!("ℹ️  没有未版本化文件需要清理"),
            Ok((removed, failed)) => {
                println!("🧹 已清理未版本化文件: 删除 {} 个, 失败 {} 个", removed, failed)
            }
            Err(e) => println!("⚠️  清理未版本化文件失败（可忽略）: {}", e),
        }

        println!("✅ dest SVN的所有本地变更已回退");
        println!("{}", "=".repeat(60));
        Ok(())
    }

    /// Reverts all local changes in the src git working tree so it is fully
    /// clean: `git reset --hard HEAD` discards tracked modifications, then
    /// `git clean -fd` removes unversioned (untracked) files and directories.
    /// Ignored files (.gitignore) such as node_modules/dist are preserved.
    pub fn revert_src_git(&self) -> Result<(), String> {
        println!();
        println!("{}", "=".repeat(60));
        println!("🔄 回退src Git工作区的所有本地改动");
        println!("{}", "=".repeat(60));
        println!("📁 源路径: {}", self.source_path);

        if self.source_path.is_empty() {
            return Err(
                "未设置source路径（请检查 version.config.json 中的 deploy.homeSourcePath / deploy.companySourcePath）"
                    .to_string(),
            );
        }

        if !git_available() {
            return Err("未找到git命令".to_string());
        }

        if !is_git_repo(&self.source_path) {
            return Err(format!("源路径不是Git仓库: {}", self.source_path));
        }

        // 1. git reset --hard HEAD: discard all tracked modifications and
        //    undo staged adds/deletes across the working tree.
        println!("⏳ 正在执行 git reset --hard HEAD ...");
        let reset = std::process::Command::new("git")
            .args(["reset", "--hard", "HEAD"])
            .current_dir(&self.source_path)
            .status()
            .map_err(|e| format!("git reset 失败: {}", e))?;
        if !reset.success() {
            return Err("git reset --hard HEAD 失败".to_string());
        }

        // 2. git clean -fd: remove unversioned (untracked) files and dirs.
        //    -x is intentionally omitted so .gitignore'd paths (node_modules,
        //    dist, build, target) are kept.
        println!("⏳ 正在执行 git clean -fd ...");
        let clean = std::process::Command::new("git")
            .args(["clean", "-fd"])
            .current_dir(&self.source_path)
            .status()
            .map_err(|e| format!("git clean 失败: {}", e))?;
        if !clean.success() {
            return Err("git clean -fd 失败".to_string());
        }

        // 3. Show git status to confirm the workspace is clean.
        let status = std::process::Command::new("git")
            .args(["status", "--porcelain"])
            .current_dir(&self.source_path)
            .output();
        if let Ok(o) = status {
            let s = String::from_utf8_lossy(&o.stdout).trim().to_string();
            if s.is_empty() {
                println!("✅ src Git工作区已干净");
            } else {
                println!("⚠️  Git状态仍有未清理的变更:\n{}", s);
            }
        }

        println!("{}", "=".repeat(60));
        Ok(())
    }
}

// ---------------------------------------------------------------------------
// Post-deploy rollback
//
// Mirrors Go's VersionManager.rollbackHTMLFile / gitCommitAndPushAfterRollback.
// After deploying CDN-hashed resources the HTML file still references the CDN
// URLs. rollback_html_file reverts it to the pre-deploy (committed) state via
// `git checkout HEAD`, then git_commit_and_push_after_rollback commits that
// rollback and pushes it.
// ---------------------------------------------------------------------------

/// Returns true if the `git` executable is on PATH.
fn git_available() -> bool {
    std::process::Command::new("git")
        .arg("--version")
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

/// Returns true if `dir` is inside a git working tree.
fn is_git_repo(dir: &str) -> bool {
    std::process::Command::new("git")
        .arg("status")
        .current_dir(dir)
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

/// Returns true if `dir` is an SVN working copy (mirrors Go's isSvnRepo).
fn is_svn_repo(dir: &str) -> bool {
    std::process::Command::new("svn")
        .arg("info")
        .current_dir(dir)
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

/// Parses `svn status` output and returns the relative paths of every
/// unversioned item (lines whose first status column is `?`).
///
/// `svn status` uses a fixed 7-column status prefix followed by a space and
/// the path, so the path always begins at byte offset 8. Reading from a fixed
/// column preserves paths that contain spaces (a whitespace split would
/// corrupt them). Lines shorter than the prefix, or without a leading `?`,
/// are ignored.
fn parse_svn_unversioned_paths(status_output: &str) -> Vec<String> {
    let mut paths = Vec::new();
    for line in status_output.lines() {
        let bytes = line.as_bytes();
        if bytes.len() >= 8 && bytes[0] == b'?' {
            // Bytes 0..8 are the ASCII status prefix, so offset 8 is always a
            // valid char boundary even when the path itself is non-ASCII.
            let path = line[8..].trim();
            if !path.is_empty() {
                paths.push(path.to_string());
            }
        }
    }
    paths
}

/// Removes the given relative paths (as printed by `svn status`) from
/// `base_dir`. Files are unlinked; directories are removed recursively.
/// Returns `(removed, failed)`; failures are logged but do not abort.
fn remove_unversioned_paths(base_dir: &str, paths: &[String]) -> (usize, usize) {
    let mut removed = 0;
    let mut failed = 0;
    for rel in paths {
        let full = path_join(base_dir, rel);
        let p = std::path::Path::new(&full);
        let res = if p.is_dir() {
            std::fs::remove_dir_all(p)
        } else {
            std::fs::remove_file(p)
        };
        match res {
            Ok(_) => removed += 1,
            Err(e) => {
                failed += 1;
                println!("⚠️  删除未版本化文件失败: {} - {}", rel, e);
            }
        }
    }
    (removed, failed)
}

/// Runs `svn status`, parses unversioned (`?`) entries, and removes them from
/// `dest_path` so the working copy is fully clean. `svn revert -R .` only
/// restores versioned files; unversioned files survive, so this step is
/// required to remove build artifacts left by a deploy. Returns
/// `(removed, failed)`.
fn clean_svn_unversioned(dest_path: &str) -> Result<(usize, usize), String> {
    let output = std::process::Command::new("svn")
        .arg("status")
        .current_dir(dest_path)
        .output()
        .map_err(|e| format!("svn status 失败: {}", e))?;
    let status_str = String::from_utf8_lossy(&output.stdout);
    let paths = parse_svn_unversioned_paths(&status_str);
    if paths.is_empty() {
        return Ok((0, 0));
    }
    Ok(remove_unversioned_paths(dest_path, &paths))
}

/// Resolves a possibly-relative path to absolute (like Go's filepath.Abs).
fn abs_path(path: &str) -> String {
    let p = std::path::Path::new(path);
    if p.is_absolute() {
        return path.to_string();
    }
    std::env::current_dir()
        .unwrap_or_else(|_| std::path::PathBuf::from("."))
        .join(path)
        .to_string_lossy()
        .to_string()
}

/// Formats the current time as YYYYMMDDHHMMSS (UTC). Used only as a fallback
/// when git log is unavailable, matching Go's time.Now().Format("20060102150405").
fn timestamp_now() -> String {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default();
    let secs = now.as_secs() as i64;

    let days = secs.div_euclid(86400);
    let sod = secs.rem_euclid(86400);
    let hour = sod / 3600;
    let min = (sod % 3600) / 60;
    let sec = sod % 60;

    // civil_from_days (Howard Hinnant's algorithm)
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

    format!("{year:04}{m:02}{d:02}{hour:02}{min:02}{sec:02}")
}

/// Reverts `html_path` to its last-committed state via `git checkout HEAD`.
/// Falls back to a plain `git checkout` if HEAD restore fails.
pub fn rollback_html_file(html_path: &str) -> Result<(), String> {
    let abs = abs_path(html_path);
    let dir = path_dir(&abs);
    let filename = path_base(&abs);

    println!();
    println!("🔄 正在回滚HTML文件: {}", filename);
    println!("  📂 工作目录: {}", dir);

    if !git_available() {
        println!("  ⚠️  未找到git命令，跳过回滚");
        return Ok(());
    }

    // 1. Restore from HEAD (ignores staged changes).
    let ok = std::process::Command::new("git")
        .args(["checkout", "HEAD", "--"])
        .arg(&filename)
        .current_dir(&dir)
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false);

    if !ok {
        // Fall back to plain checkout.
        let retry = std::process::Command::new("git")
            .args(["checkout", "--"])
            .arg(&filename)
            .current_dir(&dir)
            .output();
        match retry {
            Ok(o) if o.status.success() => {}
            Ok(o) => {
                let msg = String::from_utf8_lossy(&o.stderr);
                eprintln!("  ❌ Git回滚失败: {}", msg.trim());
                return Err(format!("git checkout failed: {}", msg.trim()));
            }
            Err(e) => {
                eprintln!("  ❌ Git回滚失败: {}", e);
                return Err(format!("git checkout failed: {}", e));
            }
        }
    }

    // 2. Print git status to confirm.
    if let Ok(output) = std::process::Command::new("git")
        .args(["status", "-s"])
        .arg(&filename)
        .current_dir(&dir)
        .output()
    {
        let status = String::from_utf8_lossy(&output.stdout).trim().to_string();
        if status.is_empty() {
            println!("  ✅ 文件状态: 已恢复至 Commit 状态 (Clean)");
        } else {
            println!("  📊 Git状态: {}", status);
        }
    }

    println!();
    println!("✅ HTML文件已回滚到CDN替换前的状态");
    Ok(())
}

/// Commits and pushes all changes after rollback.
/// Runs: git add -A, git commit, git pull --rebase, git push.
pub fn git_commit_and_push_after_rollback(html_path: &str) -> Result<(), String> {
    let abs = abs_path(html_path);
    let dir = path_dir(&abs);

    println!();
    println!("🔄 正在执行Git提交和推送...");
    println!("  📂 工作目录: {}", dir);

    if !git_available() {
        println!("  ⚠️  未找到git命令，跳过提交");
        return Ok(());
    }

    // 1. Latest commit hash + message for the rollback commit message.
    let (hash, message) = match get_latest_git_commit_for_rollback(&dir) {
        Ok(hm) => hm,
        Err(e) => {
            println!("  ⚠️  获取Git提交信息失败: {}", e);
            (timestamp_now(), String::new())
        }
    };

    let commit_msg = if !message.is_empty() {
        format!("rollback after deploy: {} ({})", message, hash)
    } else {
        format!("rollback after deploy (ref: {})", hash)
    };

    // 2. git add -A
    println!("  🔧 执行 git add -A...");
    let add = std::process::Command::new("git")
        .args(["add", "-A"])
        .current_dir(&dir)
        .output()
        .map_err(|e| format!("git add failed: {}", e))?;
    if !add.status.success() {
        let msg = String::from_utf8_lossy(&add.stderr);
        eprintln!("  ❌ Git add 失败: {}", msg.trim());
        return Err(format!("git add failed: {}", msg.trim()));
    }

    // 3. Check for changes to commit.
    let status = std::process::Command::new("git")
        .args(["status", "--porcelain"])
        .current_dir(&dir)
        .output()
        .map_err(|e| format!("git status failed: {}", e))?;
    let status_str = String::from_utf8_lossy(&status.stdout).trim().to_string();
    if status_str.is_empty() {
        println!("  ⏸️  没有变更需要提交");
        return Ok(());
    }

    // 4. git commit
    println!("  📝 执行 git commit -m \"{}\"...", commit_msg);
    let commit = std::process::Command::new("git")
        .args(["commit", "-m"])
        .arg(&commit_msg)
        .current_dir(&dir)
        .output()
        .map_err(|e| format!("git commit failed: {}", e))?;
    if !commit.status.success() {
        let msg = String::from_utf8_lossy(&commit.stderr);
        eprintln!("  ❌ Git commit 失败: {}", msg.trim());
        return Err(format!("git commit failed: {}", msg.trim()));
    }
    println!("  ✅ Git commit 成功");

    // 5. git pull --rebase (stream output to console like Go).
    println!("  🔄 执行 git pull --rebase 同步远程修改...");
    let pull = std::process::Command::new("git")
        .args(["pull", "--rebase"])
        .current_dir(&dir)
        .status()
        .map_err(|e| format!("git pull failed: {}", e))?;
    if !pull.success() {
        eprintln!("  ❌ Git pull --rebase 失败，已中止推送");
        return Err("git pull --rebase failed".to_string());
    }

    // 6. git push (stream output to console like Go).
    println!("  🚀 执行 git push...");
    let push = std::process::Command::new("git")
        .args(["push"])
        .current_dir(&dir)
        .status()
        .map_err(|e| format!("git push failed: {}", e))?;
    if !push.success() {
        eprintln!("  ❌ Git push 失败");
        return Err("git push failed".to_string());
    }
    println!("  ✅ Git push 成功");

    println!();
    println!("🎉 Git提交和推送完成！");
    Ok(())
}

/// Returns (hash, message) of the latest commit in `dir`.
fn get_latest_git_commit_for_rollback(dir: &str) -> Result<(String, String), String> {
    if !is_git_repo(dir) {
        return Err(format!("not a git repo: {}", dir));
    }

    let output = std::process::Command::new("git")
        .args(["log", "-1", "--pretty=format:%h|%s"])
        .current_dir(dir)
        .output()
        .map_err(|e| format!("git log failed: {}", e))?;

    let text = String::from_utf8_lossy(&output.stdout).to_string();
    let parts: Vec<&str> = text.splitn(2, '|').collect();
    if parts.len() != 2 {
        return Err("could not parse git commit info".to_string());
    }

    Ok((parts[0].trim().to_string(), parts[1].trim().to_string()))
}

// ---------------------------------------------------------------------------
// Tests (mirror Go test suite)
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU64, Ordering};

    fn tmp_id() -> u64 {
        static C: AtomicU64 = AtomicU64::new(0);
        C.fetch_add(1, Ordering::SeqCst)
    }

    #[test]
    fn test_deploy_cache() {
        let dir = std::env::temp_dir().join(format!("deploy_cache_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let src_file = dir.join("test.png");
        std::fs::write(&src_file, "hello world").unwrap();
        let cache_file = dir.join(".deploy-cache.json");

        // 1. Load non-existent cache -> empty
        let mut dc = load_deploy_cache(cache_file.to_str().unwrap());
        assert_eq!(dc.files.len(), 0);

        // 2. First hash (cache miss)
        let hash1 = dc.get_cached_hash(src_file.to_str().unwrap()).unwrap();
        assert!(!hash1.is_empty());
        assert_eq!(dc.files.len(), 1);

        // 3. Second hash (cache hit -> same result)
        let hash2 = dc.get_cached_hash(src_file.to_str().unwrap()).unwrap();
        assert_eq!(hash1, hash2);

        // 4. Modify file -> modTime changes -> cache miss -> different hash
        std::thread::sleep(std::time::Duration::from_millis(20));
        std::fs::write(&src_file, "hello world modified").unwrap();
        let hash3 = dc.get_cached_hash(src_file.to_str().unwrap()).unwrap();
        assert_ne!(hash3, hash1);

        // 5. Save + reload
        dc.save().unwrap();
        assert!(file_exists(cache_file.to_str().unwrap()));

        let mut dc2 = load_deploy_cache(cache_file.to_str().unwrap());
        assert_eq!(dc2.files.len(), 1);

        // 6. After reload, file unchanged -> cache hit -> same hash
        let hash4 = dc2.get_cached_hash(src_file.to_str().unwrap()).unwrap();
        assert_eq!(hash4, hash3);

        // 7. updateCache adds a second entry
        let dst_file = dir.join("copied.png");
        std::fs::write(&dst_file, "hello world modified").unwrap();
        let dst_meta = std::fs::metadata(&dst_file).unwrap();
        dc2.update_cache(
            dst_file.to_str().unwrap(),
            &hash3,
            dst_meta.len() as i64,
            mod_time_nanos(&dst_meta),
        );
       assert_eq!(dc2.files.len(), 2);

       let _ = std::fs::remove_dir_all(&dir);
   }

    #[test]
    fn test_cache_modtime_interop_with_go() {
        // Regression: Rust previously stored modTime as a JSON string, which
        // Go's encoding/json could not unmarshal into int64, forcing Go to
        // rebuild the cache from scratch every run. modTime must now be a JSON
        // number (Integer variant) that both tools can read, and the exact
        // nanosecond value must survive a save/reload round-trip.
        let dir = std::env::temp_dir().join(format!("cache_interop_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let cache_file = dir.join(".deploy-cache.json");

        let mut dc = load_deploy_cache(cache_file.to_str().unwrap());
        // Nanosecond timestamp well beyond 2^53 (f64 precision limit).
        let big_mod_time: i64 = 1_785_000_000_000_000_123;
        dc.update_cache(
            dir.join("app.js").to_str().unwrap(),
            "deadbeef",
            42,
            big_mod_time,
        );
        dc.save().unwrap();

        // The written JSON must contain modTime as a bare number, not a string.
        let raw = std::fs::read_to_string(cache_file.to_str().unwrap()).unwrap();
        assert!(
            raw.contains("\"modTime\": 1785000000000000123"),
            "modTime must be a JSON number for Go interop, got: {}",
            raw
        );
        assert!(
            !raw.contains("\"modTime\": \"1785000000000000123\""),
            "modTime must NOT be a string"
        );

        // Reload and verify exact preservation.
        let dc2 = load_deploy_cache(cache_file.to_str().unwrap());
        let entry = dc2
            .files
            .get(&clean_key(dir.join("app.js").to_str().unwrap()))
            .expect("cache entry should exist after reload");
        assert_eq!(
            entry.mod_time, big_mod_time,
            "modTime must survive save/reload exactly"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_validate_cdn_resources() {
        let dir = std::env::temp_dir().join(format!("validate_cdn_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();
        let cdn_domain = "https://cdn.example.com";

        let html = format!(
            "<link href=\"{}/css/style.css\">\n<script src=\"{}/js/app.js\"></script>",
            cdn_domain, cdn_domain
        );
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, &html).unwrap();

        // Create dest files so validation passes
        let dest_dir = dir.join("dest");
        std::fs::create_dir_all(dest_dir.join("css")).unwrap();
        std::fs::create_dir_all(dest_dir.join("js")).unwrap();
        std::fs::write(dest_dir.join("css").join("style.css"), "body{}").unwrap();
        std::fs::write(dest_dir.join("js").join("app.js"), "1").unwrap();

        let dm = DeployManager {
            config: DeployConfig::default(),
            source_path: String::new(),
            dest_path: dest_dir.to_string_lossy().to_string(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dir.join(".deploy-cache.json").to_str().unwrap()),
        };

        assert!(dm
            .validate_cdn_resources(html_path.to_str().unwrap(), cdn_domain)
            .is_ok());

        // Remove a file -> validation should fail
        std::fs::remove_file(dest_dir.join("css").join("style.css")).unwrap();
        assert!(dm
            .validate_cdn_resources(html_path.to_str().unwrap(), cdn_domain)
            .is_err());

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_clean_hash_files() {
        let dir = std::env::temp_dir().join(format!("clean_hash_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();

        std::fs::write(dir.join("style.aaaabbbb.css"), "keep").unwrap();
        std::fs::write(dir.join("style.ccccdddd.css"), "old").unwrap();
        std::fs::write(dir.join("style.css"), "base").unwrap();
        std::fs::write(dir.join("other.css"), "unrelated").unwrap();

        let dm = DeployManager {
            config: DeployConfig::default(),
            source_path: String::new(),
            dest_path: dir.to_string_lossy().to_string(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dir.join(".deploy-cache.json").to_str().unwrap()),
        };

        let dest_path = dir.join("style.css");
        let deleted = dm.clean_hash_files(dest_path.to_str().unwrap(), "style.aaaabbbb.css");
        assert_eq!(deleted, 1);
        assert!(file_exists(
            dir.join("style.aaaabbbb.css").to_str().unwrap()
        ));
        assert!(!file_exists(
            dir.join("style.ccccdddd.css").to_str().unwrap()
        ));
        assert!(file_exists(dir.join("style.css").to_str().unwrap()));
        assert!(file_exists(dir.join("other.css").to_str().unwrap()));

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_copy_file_with_versions() {
        let src_dir = std::env::temp_dir().join(format!("cfv_src_{}", tmp_id()));
        let dst_dir = std::env::temp_dir().join(format!("cfv_dst_{}", tmp_id()));
        std::fs::create_dir_all(&src_dir).unwrap();
        std::fs::create_dir_all(&dst_dir).unwrap();

        // Source: base file + one hash version (same content)
        std::fs::write(src_dir.join("app.js"), "console.log(1)").unwrap();
        std::fs::write(src_dir.join("app.aaaabbbb.js"), "console.log(1)").unwrap();

        let mut dm = DeployManager {
            config: DeployConfig::default(),
            source_path: src_dir.to_string_lossy().to_string(),
            dest_path: dst_dir.to_string_lossy().to_string(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dst_dir.join(".deploy-cache.json").to_str().unwrap()),
        };

        let (copied, _skipped) = dm
            .copy_file_with_versions("app.js", dst_dir.join("app.js").to_str().unwrap())
            .unwrap();
        assert!(copied > 0, "expected at least 1 file copied");

        // Base file should exist in dest
        assert!(file_exists(dst_dir.join("app.js").to_str().unwrap()));
        // Hash version should exist in dest
        assert!(file_exists(
            dst_dir.join("app.aaaabbbb.js").to_str().unwrap()
        ));

        let _ = std::fs::remove_dir_all(&src_dir);
        let _ = std::fs::remove_dir_all(&dst_dir);
    }

    #[test]
    fn test_wildcard_path_copies_subdirectory_files() {
        // Regression for the gift-carousel2 production incident: a wildcard
        // path like "/images/xdrNormal/202505/*" must walk into subdirectories
        // (mirroring Go's filepath.WalkDir). The previous Rust deploy used a
        // non-recursive read_dir, so a hashed image referenced from CSS and
        // living in a nested folder (images/.../202505/new/gift-carousel2.<hash>.png)
        // was never copied to dest, even though its CSS/JS siblings were.
        let id = tmp_id();
        let src_root = std::env::temp_dir().join(format!("wc_src_{}", id));
        let dst_root = std::env::temp_dir().join(format!("wc_dst_{}", id));
        let img_dir = src_root.join("images/xdrNormal/202505/new");
        std::fs::create_dir_all(&img_dir).unwrap();
        // immediate-child file directly under the wildcard root
        std::fs::write(src_root.join("images/xdrNormal/202505/icon.png"), "i").unwrap();
        // base + hashed image nested one level deeper
        std::fs::write(img_dir.join("gift-carousel2.png"), "carousel").unwrap();
        std::fs::write(img_dir.join("gift-carousel2.114b07c2.png"), "carousel").unwrap();
        std::fs::create_dir_all(&dst_root).unwrap();

        let mut dm = DeployManager {
            config: DeployConfig::default(),
            source_path: src_root.to_string_lossy().to_string(),
            dest_path: dst_root.to_string_lossy().to_string(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dst_root.join(".deploy-cache.json").to_str().unwrap()),
        };

        let (copied, _skipped, failed) = dm
            .handle_wildcard_path("/images/xdrNormal/202505/*")
            .expect("wildcard deploy should succeed");

        assert_eq!(failed, 0, "no file should fail to copy");
        assert!(copied > 0, "expected files to be copied");

        // immediate-child file copied
        assert!(file_exists(
            dst_root
                .join("images/xdrNormal/202505/icon.png")
                .to_str()
                .unwrap()
        ));
        // nested base + hashed image copied (the production incident)
        assert!(
            file_exists(
                dst_root
                    .join("images/xdrNormal/202505/new/gift-carousel2.png")
                    .to_str()
                    .unwrap()
            ),
            "nested base image must be deployed"
        );
        assert!(
            file_exists(
                dst_root
                    .join("images/xdrNormal/202505/new/gift-carousel2.114b07c2.png")
                    .to_str()
                    .unwrap()
            ),
            "nested hashed image must be deployed (production incident)"
        );

       let _ = std::fs::remove_dir_all(&src_root);
       let _ = std::fs::remove_dir_all(&dst_root);
  }

   // ===================================================================
   // CDN validation regression tests
   //
   // These tests verify that validate_cdn_resources correctly handles
   // various URL formats and catches the double-prefix bug.
   // ===================================================================

   #[test]
   fn test_wildcard_deploy_cleans_old_hash_and_copies_nested() {
        // End-to-end regression for the production incident: deploy via
        // wildcard must (1) copy nested hashed images to dest and (2) clean
        // old hash files from dest, exactly like Go's filepath.WalkDir +
        // cleanHashFiles. The old Rust binary used non-recursive read_dir so
        // nested files were skipped, and old hash files were left behind.
        let id = tmp_id();
        let src_root = std::env::temp_dir().join(format!("e2e_src_{}", id));
        let dst_root = std::env::temp_dir().join(format!("e2e_dst_{}", id));

        // Source tree mimicking the real layout.
        let src_nested = src_root.join("images/xdrNormal/202505/new");
        std::fs::create_dir_all(&src_nested).unwrap();
        std::fs::write(src_nested.join("gift-carousel2.png"), "new-content").unwrap();
        std::fs::write(src_nested.join("gift-carousel2.114b07c2.png"), "new-content").unwrap();

        // Dest already has an OLD hash file that must be cleaned.
        let dst_nested = dst_root.join("images/xdrNormal/202505/new");
        std::fs::create_dir_all(&dst_nested).unwrap();
        std::fs::write(dst_nested.join("gift-carousel2.deadbeef.png"), "stale").unwrap();

        let mut dm = DeployManager {
            config: DeployConfig::default(),
            source_path: src_root.to_string_lossy().to_string(),
            dest_path: dst_root.to_string_lossy().to_string(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dst_root.join(".deploy-cache.json").to_str().unwrap()),
        };

        let (copied, _skipped, failed) = dm
            .handle_wildcard_path("/images/xdrNormal/202505/*")
            .expect("wildcard deploy should succeed");

        assert_eq!(failed, 0, "no file should fail to copy");
        assert!(copied > 0, "expected files to be copied");

        // Nested base + new hash deployed to dest.
        assert!(
            file_exists(dst_nested.join("gift-carousel2.png").to_str().unwrap()),
            "nested base image must be deployed"
        );
        assert!(
            file_exists(dst_nested.join("gift-carousel2.114b07c2.png").to_str().unwrap()),
            "nested hashed image must be deployed"
        );
        // Old hash file cleaned from dest.
        assert!(
            !file_exists(dst_nested.join("gift-carousel2.deadbeef.png").to_str().unwrap()),
            "old hash file must be cleaned from dest (production incident)"
        );

        let _ = std::fs::remove_dir_all(&src_root);
        let _ = std::fs::remove_dir_all(&dst_root);
    }

   // ===================================================================
   // CDN validation regression tests (continued)
   //
   // These tests verify that validate_cdn_resources correctly handles
   // various URL formats and catches the double-prefix bug.
   // ===================================================================

    #[test]
    fn test_validate_cdn_with_path_prefix() {
        // Validation should work when cdn_path_prefix is configured
        // (matching the real config where cdnDomain == cdnPathPrefix).
        let dir = std::env::temp_dir().join(format!("vp_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();

        let cdn_domain = "https://cdn.example.com/v1/res";
        let html = format!(
            "<link href=\"{}/components/xdrsignNew/index.aaaabbbb.css\">\n\
             <script src=\"{}/components/xdrsignNew/app.ccccdddd.js\"></script>",
            cdn_domain, cdn_domain
        );
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, &html).unwrap();

        // Create dest files at the expected paths
        let dest_dir = dir.join("dest");
        let comp_dir = dest_dir.join("components").join("xdrsignNew");
        std::fs::create_dir_all(&comp_dir).unwrap();
        std::fs::write(comp_dir.join("index.aaaabbbb.css"), "body{}").unwrap();
        std::fs::write(comp_dir.join("app.ccccdddd.js"), "1").unwrap();

        let dm = DeployManager {
            config: DeployConfig {
                cdn_path_prefix: cdn_domain.to_string(),
                ..Default::default()
            },
            source_path: String::new(),
            dest_path: dest_dir.to_string_lossy().to_string(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dir.join(".deploy-cache.json").to_str().unwrap()),
        };

        assert!(
            dm.validate_cdn_resources(html_path.to_str().unwrap(), cdn_domain).is_ok(),
            "validation should pass when files exist"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_validate_cdn_catches_double_prefix() {
        // Validation should FAIL when HTML contains double-prefixed URLs
        // (the exact bug the user encountered: cdnDomain/cdnDomain/components/...).
        let dir = std::env::temp_dir().join(format!("vdp_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();

        let cdn_domain = "https://cdn.example.com/v1/res";
        // Double-prefixed URL: cdnDomain + / + cdnDomain + /components/...
        let double_url = format!("{}/{}", cdn_domain, cdn_domain);
        let html = format!(
            "<link href=\"{}/components/xdrsignNew/index.aaaabbbb.css\">",
            double_url
        );
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, &html).unwrap();

        // Create the correct dest file (single-prefix path)
        let dest_dir = dir.join("dest");
        let comp_dir = dest_dir.join("components").join("xdrsignNew");
        std::fs::create_dir_all(&comp_dir).unwrap();
        std::fs::write(comp_dir.join("index.aaaabbbb.css"), "body{}").unwrap();

        let dm = DeployManager {
            config: DeployConfig::default(),
            source_path: String::new(),
            dest_path: dest_dir.to_string_lossy().to_string(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dir.join(".deploy-cache.json").to_str().unwrap()),
        };

        let result = dm.validate_cdn_resources(html_path.to_str().unwrap(), cdn_domain);
        assert!(
            result.is_err(),
            "validation should fail for double-prefixed URL"
        );
        let err = result.unwrap_err();
        assert!(
            err.contains("file missing"),
            "error should mention 'file missing', got: {err}"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_validate_cdn_query_params_stripped() {
        // Validation should strip query parameters before checking files.
        let dir = std::env::temp_dir().join(format!("vqp_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();

        let cdn_domain = "https://cdn.example.com";
        let html = format!(
            "<link href=\"{}/css/style.css?v=20260727\">",
            cdn_domain
        );
        let html_path = dir.join("index.html");
        std::fs::write(&html_path, &html).unwrap();

        let dest_dir = dir.join("dest");
        std::fs::create_dir_all(dest_dir.join("css")).unwrap();
        std::fs::write(dest_dir.join("css").join("style.css"), "body{}").unwrap();

        let dm = DeployManager {
            config: DeployConfig::default(),
            source_path: String::new(),
            dest_path: dest_dir.to_string_lossy().to_string(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dir.join(".deploy-cache.json").to_str().unwrap()),
        };

        assert!(
            dm.validate_cdn_resources(html_path.to_str().unwrap(), cdn_domain).is_ok(),
            "validation should pass when query params are stripped"
        );

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_parse_svn_unversioned_paths() {
        // svn status: 7 status columns + space + path. Only '?' rows are
        // unversioned; M/A/D rows are versioned changes handled by svn revert.
        let status = "?       css/xdrNormal.688db72b.css\nM       css/xdrNormal.css\n?       images/xdrNormal/202505/new/gift-carousel2.114b07c2.png\n?       images/xdrNormal/202505/new/gift-carousel2.png\nA       scripts/js/xdrNormal.64afb25c.js\n?       scripts/js/xdrNormal.64afb25c.js\n";
        let paths = parse_svn_unversioned_paths(status);
        assert_eq!(
            paths,
            vec![
                "css/xdrNormal.688db72b.css",
                "images/xdrNormal/202505/new/gift-carousel2.114b07c2.png",
                "images/xdrNormal/202505/new/gift-carousel2.png",
                "scripts/js/xdrNormal.64afb25c.js",
            ]
        );
    }

    #[test]
    fn test_parse_svn_unversioned_paths_skips_non_question_and_footer() {
        // Blank lines, the status footer, and short/garbled lines are ignored;
        // only a well-formed '?' row yields a path.
        let status = "\nM       tracked.css\nStatus against revision:      42\n?\n?       ok.txt\n";
        let paths = parse_svn_unversioned_paths(status);
        assert_eq!(paths, vec!["ok.txt"]);
    }

    #[test]
    fn test_remove_unversioned_paths() {
        let dir = std::env::temp_dir().join(format!("unversion_{}", tmp_id()));
        std::fs::create_dir_all(dir.join("css")).unwrap();
        std::fs::create_dir_all(dir.join("images/xdrNormal/202505/new")).unwrap();
        std::fs::write(dir.join("css").join("xdr.688db72b.css"), "x").unwrap();
        std::fs::write(
            dir.join("images/xdrNormal/202505/new")
                .join("gift-carousel2.114b07c2.png"),
            "y",
        )
        .unwrap();
        std::fs::write(dir.join("keep.css"), "z").unwrap();

        // A whole unversioned directory and a single file, exactly as svn
        // status prints them (one entry per unversioned item, no recursion).
        let paths = vec![
            "css/xdr.688db72b.css".to_string(),
            "images/xdrNormal/202505/new".to_string(),
        ];
        let (removed, failed) = remove_unversioned_paths(dir.to_str().unwrap(), &paths);
        assert_eq!(failed, 0);
        assert_eq!(removed, 2);
        assert!(!dir.join("css/xdr.688db72b.css").exists());
        assert!(!dir.join("images/xdrNormal/202505/new").exists());
        assert!(dir.join("keep.css").exists());

        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn test_revert_src_git_errors_on_empty_source() {
        let dm = DeployManager {
            config: DeployConfig::default(),
            source_path: String::new(),
            dest_path: String::new(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(""),
        };
        let res = dm.revert_src_git();
        assert!(res.is_err(), "empty source path should error");
    }

    #[test]
    fn test_revert_src_git_cleans_workspace() {
        // Integration test: requires a real git binary. Builds a throwaway
        // repo, dirties it (tracked modification + untracked file/dir), then
        // verifies revert_src_git leaves a clean working tree.
        if !git_available() {
            eprintln!("[skip] git not available");
            return;
        }
        let dir = std::env::temp_dir().join(format!("revert_git_{}", tmp_id()));
        std::fs::create_dir_all(&dir).unwrap();

        let run_git = |args: &[&str]| {
            std::process::Command::new("git")
                .args(args)
                .current_dir(&dir)
                .env("GIT_AUTHOR_NAME", "test")
                .env("GIT_AUTHOR_EMAIL", "test@test.com")
                .env("GIT_COMMITTER_NAME", "test")
                .env("GIT_COMMITTER_EMAIL", "test@test.com")
                .output()
                .expect("git command failed")
        };

        run_git(&["init"]);
        run_git(&["config", "user.name", "test"]);
        run_git(&["config", "user.email", "test@test.com"]);

        // Initial commit of a tracked file.
        std::fs::write(dir.join("tracked.txt"), "v1").unwrap();
        run_git(&["add", "tracked.txt"]);
        run_git(&["commit", "-m", "init"]);

        // Dirty the working tree: modify the tracked file + add untracked
        // file/dir (mimicking hashed build artifacts left by a deploy).
        std::fs::write(dir.join("tracked.txt"), "modified").unwrap();
        std::fs::write(dir.join("untracked.688db72b.css"), "u").unwrap();
        std::fs::create_dir_all(dir.join("untracked_dir")).unwrap();
        std::fs::write(dir.join("untracked_dir").join("inside.png"), "i").unwrap();

        let dm = DeployManager {
            config: DeployConfig::default(),
            source_path: dir.to_string_lossy().to_string(),
            dest_path: String::new(),
            debug_mode: false,
            folder_opened: false,
            cache: load_deploy_cache(dir.join(".deploy-cache.json").to_str().unwrap()),
        };
        dm.revert_src_git().expect("revert_src_git should succeed");

        // Tracked file restored to committed content.
        assert_eq!(
            std::fs::read_to_string(dir.join("tracked.txt")).unwrap(),
            "v1",
            "tracked modification must be reverted"
        );
        // Untracked file and dir removed.
        assert!(
            !dir.join("untracked.688db72b.css").exists(),
            "untracked file must be removed"
        );
        assert!(
            !dir.join("untracked_dir").exists(),
            "untracked dir must be removed"
        );

        // Working tree must be clean (porcelain output empty).
        let st = run_git(&["status", "--porcelain"]);
        assert!(
            String::from_utf8_lossy(&st.stdout).trim().is_empty(),
            "workspace should be clean, got: {}",
            String::from_utf8_lossy(&st.stdout)
        );

        let _ = std::fs::remove_dir_all(&dir);
    }
}
