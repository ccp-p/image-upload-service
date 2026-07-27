//! Deploy functionality: file copying with hash versions, CDN resource validation.
//! Mirrors the Go DeployManager in cmd/hashCdn/main.go.
//! Uses a persistent on-disk cache to avoid recomputing MD5 on unchanged files.

use std::collections::HashMap;
use std::path::PathBuf;

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
/// missing or unparseable. modTime is stored as a string in the JSON to
/// preserve i64 nanosecond precision (f64 would lose bits past 2^53), but we
/// also accept the Go-format numeric form for interoperability.
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
            // modTime: prefer string form (Rust), fall back to number (Go format)
            let mod_time = val
                .get_str("modTime")
                .and_then(|s| s.parse::<i64>().ok())
                .or_else(|| val.get_num("modTime").map(|n| n as i64))
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

/// Serialises the cache to pretty JSON, storing modTime as a string so that
/// i64 nanosecond timestamps survive a save/reload round-trip exactly.
fn serialize_cache(files: &HashMap<String, FileCacheEntry>) -> String {
    let mut entries: Vec<(String, JsonValue)> = Vec::new();
    for (k, v) in files {
        let entry = JsonValue::Object(vec![
            ("hash".to_string(), JsonValue::String(v.hash.clone())),
            ("size".to_string(), JsonValue::Number(v.size as f64)),
            (
                "modTime".to_string(),
                JsonValue::String(v.mod_time.to_string()),
            ),
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
    pub fn run(&mut self, single_html_file: &str, cdn_domain: &str) -> Result<(), String> {
        println!("🚀 开始部署操作...");
        println!("📂 源路径: {}", self.source_path);
        println!("📂 目标路径: {}", self.dest_path);
        println!();
        println!("💾 已加载文件缓存: {} 个条目", self.cache.files.len());
        println!();
        println!("📦 开始复制文件...");

        let file_paths = self.config.file_paths.clone();
        let mut total_copied = 0;
        let mut total_skipped = 0;
        let mut total_failed = 0;

        for file_path in &file_paths {
            if file_path.ends_with("/*") {
                let dir_rel = file_path.trim_end_matches("/*");
                let src_dir = path_join(&self.source_path, dir_rel);
                if !file_exists(&src_dir) {
                    println!("⚠️  目录不存在: {}", src_dir);
                    total_failed += 1;
                    continue;
                }
                if let Ok(entries) = std::fs::read_dir(&src_dir) {
                    for entry in entries.flatten() {
                        if entry.file_type().map(|t| t.is_file()).unwrap_or(false) {
                            let name = entry.file_name().to_string_lossy().to_string();
                            let rel = format!("{}/{}", dir_rel, name);
                            let dest = path_join(&self.dest_path, &rel);
                            match self.copy_file_with_versions(&rel, &dest) {
                                Ok((c, s)) => {
                                    total_copied += c;
                                    total_skipped += s;
                                }
                                Err(e) => {
                                    println!("⚠️  处理失败: {} - {}", rel, e);
                                    total_failed += 1;
                                }
                            }
                        }
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

    // ===================================================================
    // CDN validation regression tests
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
}
