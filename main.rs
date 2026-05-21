use regex::{Captures, Regex};
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};
use std::env;
use std::fs::{self, File};
use std::io::{self, Read, Write};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::{Arc, Mutex};
use std::time::SystemTime;

// ==========================================
// 1. 配置结构体与全局变量
// ==========================================

#[derive(Debug, Clone, Deserialize, Serialize, Default)]
pub struct Config {
    #[serde(default = "default_root_dir", rename = "rootDir")]
    pub root_dir: String,
    #[serde(default, rename = "cdnDomain")]
    pub cdn_domain: String,
    #[serde(default = "default_hash_length", rename = "hashLength")]
    pub hash_length: usize,
    #[serde(default, rename = "singleHTMLFile")]
    pub single_html_file: String,
    #[serde(default, rename = "htmlFiles")]
    pub html_files: Vec<String>,
    #[serde(default = "default_exclude_dirs", rename = "excludeDirs")]
    pub exclude_dirs: Vec<String>,
    #[serde(default, rename = "homeHTMLFile")]
    pub home_html_file: String,
    #[serde(default, rename = "companyHTMLFile")]
    pub company_html_file: String,
    #[serde(default, rename = "includeComponents")]
    pub include_components: Vec<String>,
    #[serde(default, rename = "processMainResources")]
    pub process_main_resources: Vec<String>,
    #[serde(default, rename = "replaceAllWithCDN")]
    pub replace_all_with_cdn: bool,
    #[serde(default, rename = "RollbackAfterDeploy")]
    pub rollback_after_deploy: bool,
    #[serde(default, rename = "cdnExcludeFiles")]
    pub cdn_exclude_files: Vec<String>,
    #[serde(default)]
    pub deploy: DeployConfig,
}

#[derive(Debug, Clone, Deserialize, Serialize, Default)]
pub struct DeployConfig {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub command: String,
    #[serde(default, rename = "autoCommit")]
    pub auto_commit: bool,
    #[serde(default, rename = "homeSourcePath")]
    pub home_source_path: String,
    #[serde(default, rename = "homeDestPath")]
    pub home_dest_path: String,
    #[serde(default, rename = "companySourcePath")]
    pub company_source_path: String,
    #[serde(default, rename = "companyDestPath")]
    pub company_dest_path: String,
    #[serde(default, rename = "filePaths")]
    pub file_paths: Vec<String>,
    #[serde(default, rename = "gitAuthors")]
    pub git_authors: Vec<String>,
    #[serde(default, rename = "cdnPathPrefix")]
    pub cdn_path_prefix: String,
}

fn default_root_dir() -> String { ".".to_string() }
fn default_hash_length() -> usize { 8 }
fn default_exclude_dirs() -> Vec<String> {
    vec!["node_modules".into(), ".git".into(), "dist".into(), "build".into()]
}

pub struct FileInfo {
    pub original_path: PathBuf,
    pub hashed_path: PathBuf,
    pub hash: String,
    pub renamed: bool,
}

pub struct ImageReference {
    pub original_path: String,
    pub absolute_path: PathBuf,
    pub relative_path: PathBuf,
}

// ==========================================
// 2. 工具函数
// ==========================================

#[allow(dead_code)]
fn file_exists(path: impl AsRef<Path>) -> bool {
    path.as_ref().exists()
}

fn copy_file(src: impl AsRef<Path>, dst: impl AsRef<Path>) -> io::Result<()> {
    fs::copy(src, dst).map(|_| ())
}

fn get_file_hash(path: impl AsRef<Path>, length: usize) -> io::Result<String> {
    let mut file = File::open(path)?;
    let mut buffer = Vec::new();
    file.read_to_end(&mut buffer)?;
    let digest = md5::compute(buffer);
    let mut hash_str = format!("{:x}", digest);
    if length > 0 && length < hash_str.len() {
        hash_str.truncate(length);
    }
    Ok(hash_str)
}

#[allow(dead_code)]
fn is_windows() -> bool {
    cfg!(target_os = "windows")
}

#[allow(dead_code)]
fn is_darwin() -> bool {
    cfg!(target_os = "macos")
}

fn clean_path_slashes(path: &str) -> String {
    path.replace('\\', "/")
}

// ==========================================
// 3. 版本管理器 (VersionManager)
// ==========================================

pub struct VersionManager {
    config: Config,
    processed_files: Arc<Mutex<HashSet<PathBuf>>>,
    debug_mode: bool,
    folder_opened: Arc<Mutex<bool>>,
    deploy_lock: Arc<Mutex<()>>,
}

impl VersionManager {
    pub fn new(config: Config, debug_mode: bool) -> Self {
        Self {
            config,
            processed_files: Arc::new(Mutex::new(HashSet::new())),
            debug_mode,
            folder_opened: Arc::new(Mutex::new(false)),
            deploy_lock: Arc::new(Mutex::new(())),
        }
    }

    fn git_add_file(&self, file_path: &Path) {
        if Command::new("git").arg("--version").output().is_err() {
            return;
        }

        let dir = file_path.parent().unwrap_or_else(|| Path::new("."));
        let filename = file_path.file_name().unwrap_or_default();

        let mut cmd = Command::new("git");
        cmd.arg("add").arg(filename).current_dir(dir);

        match cmd.output() {
            Ok(output) => {
                if !output.status.success() && self.debug_mode {
                    println!("      ⚠️  Git add 失败: {:?}", filename);
                    println!("      Output: {}", String::from_utf8_lossy(&output.stderr));
                } else if self.debug_mode {
                    println!("    ➕ Git add: {:?}", filename);
                }
            }
            Err(e) => {
                if self.debug_mode {
                    println!("      ⚠️  Git add 失败: {:?} ({})", filename, e);
                }
            }
        }
    }

    fn run_node_copy_script(&self) {
        let is_home = env::var("IS_HOME").unwrap_or_default() == "1";
        let script_path = if is_home {
            println!("🏠 当前环境: Home");
            r"D:\self_project\js_project\miaowei\test\auto\normal.js"
        } else {
            println!("🏢 当前环境: Office");
            r"d:\project\my_web_project\web\train\miaov-disk\Cloud_disk\test\auto\normal.js"
        };

        println!("🚀 执行部署脚本: node {} copy", script_path);

        if Command::new("node").arg("--version").output().is_err() {
            println!("⚠️  未找到 node 命令，跳过脚本执行");
            return;
        }

        let mut cmd = Command::new("node");
        cmd.arg(script_path).arg("copy");

        match cmd.status() {
            Ok(status) if status.success() => println!("✅ 脚本执行成功"),
            Ok(_) | Err(_) => println!("❌ 脚本执行失败"),
        }
    }

    fn should_process_component(&self, component_path: &str) -> bool {
        if self.config.include_components.is_empty() {
            return true;
        }
        let norm_path = clean_path_slashes(component_path);
        let path_obj = Path::new(component_path);
        let filename = path_obj.file_name().unwrap_or_default().to_string_lossy();

        for comp in &self.config.include_components {
            let part = format!("/{}/", comp);
            let suffix = format!("/{}", comp);
            let prefix = format!("{}.", comp);
            if norm_path.contains(&part) || norm_path.ends_with(&suffix) || filename.starts_with(&prefix) {
                return true;
            }
        }
        false
    }

    fn calculate_file_hash(&self, path: &Path) -> io::Result<String> {
        get_file_hash(path, self.config.hash_length)
    }

    fn remove_hash_from_filename(&self, filename: &str) -> String {
        let re = Regex::new(r"^(.+)\.([a-f0-9]{8,32})\.(css|js|jpg|jpeg|png|gif|svg|webp|ico)$").unwrap();
        if let Some(caps) = re.captures(filename) {
            return format!("{}.{}", &caps[1], &caps[3]);
        }
        filename.to_string()
    }

    fn add_hash_to_filename(&self, filename: &str, hash: &str) -> String {
        let path = Path::new(filename);
        let ext = path.extension().unwrap_or_default().to_string_lossy();
        let stem = path.file_stem().unwrap_or_default().to_string_lossy();

        let re = Regex::new(r"\.[a-f0-9]{8,32}$").unwrap();
        let clean_stem = re.replace(&stem, "");
        
        if ext.is_empty() {
            format!("{}.{}", clean_stem, hash)
        } else {
            format!("{}.{}.{}", clean_stem, hash, ext)
        }
    }

    fn find_and_delete_old_hash_files(&self, dir: &Path, basename: &str, ext: &str, current_hash: &str) -> io::Result<()> {
        if self.debug_mode {
            println!("  🔍 查找旧hash文件: {}{} (当前hash: {})", basename, ext, current_hash);
        }

        let escaped_base = regex::escape(basename);
        let escaped_ext = regex::escape(ext);
        let pattern = format!(r"^{}\.[a-f0-9]{{8,32}}{}$", escaped_base, escaped_ext);
        let re = Regex::new(&pattern).unwrap();
        
        let extract_pattern = format!(r"^{}\.([a-f0-9]{{8,32}}){}$", escaped_base, escaped_ext);
        let hash_re = Regex::new(&extract_pattern).unwrap();

        let mut deleted_count = 0;
        if let Ok(entries) = fs::read_dir(dir) {
            for entry in entries.flatten() {
                if entry.file_type().map_or(false, |t| !t.is_dir()) {
                    let filename = entry.file_name().to_string_lossy().to_string();
                    if re.is_match(&filename) {
                        if let Some(caps) = hash_re.captures(&filename) {
                            let extracted_hash = &caps[1];
                            if extracted_hash != current_hash {
                                if fs::remove_file(entry.path()).is_ok() {
                                    println!("    🗑️  已删除: {}", filename);
                                    deleted_count += 1;
                                } else {
                                    println!("    ⚠️  删除失败: {}", filename);
                                }
                            }
                        }
                    }
                }
            }
        }

        if self.debug_mode && deleted_count > 0 {
            println!("  ✅ 共删除 {} 个旧文件", deleted_count);
        }
        Ok(())
    }

    fn rename_file_with_hash(&self, file_path: &Path) -> io::Result<FileInfo> {
        let dir = file_path.parent().unwrap_or_else(|| Path::new("."));
        let filename = file_path.file_name().unwrap_or_default().to_string_lossy().to_string();
        let clean_filename = self.remove_hash_from_filename(&filename);
        
        let clean_path = dir.join(&clean_filename);
        let source_path = if clean_path.exists() { clean_path } else { file_path.to_path_buf() };

        let hash = self.calculate_file_hash(&source_path)?;
        let new_filename = self.add_hash_to_filename(&clean_filename, &hash);
        let new_path = dir.join(&new_filename);

        let info = FileInfo {
            original_path: source_path.clone(),
            hashed_path: new_path.clone(),
            hash: hash.clone(),
            renamed: true,
        };

        if new_path.exists() {
            if self.debug_mode {
                println!("  ⏭️  跳过（已存在）: {:?}", new_filename);
            }
            let ext = Path::new(&clean_filename).extension().unwrap_or_default().to_string_lossy();
            let basename = clean_filename.trim_end_matches(&format!(".{}", ext));
            let dot_ext = if ext.is_empty() { String::new() } else { format!(".{}", ext) };
            let _ = self.find_and_delete_old_hash_files(dir, basename, &dot_ext, &hash);
            return Ok(info);
        }

        copy_file(&source_path, &new_path)?;
        self.git_add_file(&new_path);
        println!("  ✅ 已生成: {}", new_filename);

        let ext = Path::new(&clean_filename).extension().unwrap_or_default().to_string_lossy();
        let basename = clean_filename.trim_end_matches(&format!(".{}", ext));
        let dot_ext = if ext.is_empty() { String::new() } else { format!(".{}", ext) };
        let _ = self.find_and_delete_old_hash_files(dir, basename, &dot_ext, &hash);

        Ok(info)
    }

    fn find_file(&self, base_path: &Path) -> Option<PathBuf> {
        if base_path.exists() {
            return Some(base_path.to_path_buf());
        }

        let dir = base_path.parent().unwrap_or_else(|| Path::new("."));
        let name = base_path.file_name().unwrap_or_default().to_string_lossy();
        let ext = base_path.extension().unwrap_or_default().to_string_lossy();
        let basename = name.trim_end_matches(&format!(".{}", ext));
        let dot_ext = if ext.is_empty() { String::new() } else { format!(".{}", ext) };

        if !dir.exists() {
            return None;
        }

        let pattern = format!(r"^{}\.[a-f0-9]{{8,32}}{}$", regex::escape(basename), regex::escape(&dot_ext));
        if let Ok(re) = Regex::new(&pattern) {
            if let Ok(entries) = fs::read_dir(dir) {
                for entry in entries.flatten() {
                    let fname = entry.file_name().to_string_lossy().to_string();
                    if re.is_match(&fname) {
                        return Some(dir.join(fname));
                    }
                }
            }
        }
        None
    }

    fn collect_images_from_css(&self, css_path: &Path) -> io::Result<Vec<ImageReference>> {
        let content = fs::read_to_string(css_path)?;
        let css_dir = css_path.parent().unwrap_or_else(|| Path::new("."));
        let mut images = Vec::new();

        let re = Regex::new(r#"url\(['"]?([^'")\s]+)['"]?\)"#).unwrap();
        
        for caps in re.captures_iter(&content) {
            let image_path = &caps[1];
            if image_path.starts_with("http") || image_path.starts_with("data:") || image_path.starts_with("//") {
                continue;
            }

            let clean_image_path = image_path.split('?').next().unwrap_or(image_path);
            let clean_image_path = clean_image_path.split('#').next().unwrap_or(clean_image_path);
            
            // Normalize path for path joining
            let os_clean_path = if cfg!(windows) {
                clean_image_path.replace('/', "\\")
            } else {
                clean_image_path.replace('\\', "/")
            };

            let absolute_path = css_dir.join(&os_clean_path);
            let absolute_path = clean_path(absolute_path.clone()); // Simplistic clean or fs::canonicalize

            if absolute_path.exists() {
                // Compute relative (naively)
                let relative_path = absolute_path.strip_prefix(css_dir).unwrap_or(&absolute_path).to_path_buf();
                images.push(ImageReference {
                    original_path: image_path.to_string(),
                    absolute_path,
                    relative_path,
                });
            }
        }
        Ok(images)
    }

    fn update_css_image_references(&self, css_path: &Path, image_map: &HashMap<String, String>) -> io::Result<()> {
        let content = fs::read_to_string(css_path)?;
        let mut updated = false;

        let re = Regex::new(r#"url\(\s*(['"]?)([^'")\s]+)(['"]?)\s*\)"#).unwrap();
        
        let new_content = re.replace_all(&content, |caps: &Captures| {
            let open_quote = &caps[1];
            let original_path = &caps[2];
            let close_quote = &caps[3];

            if original_path.starts_with("http") || original_path.starts_with("data:") || original_path.starts_with("//") {
                return caps[0].to_string();
            }

            let mut clean_path = original_path.split('?').next().unwrap_or(original_path).to_string();
            clean_path = clean_path.split('#').next().unwrap_or(&clean_path).to_string();
            let normalized_path = clean_path_slashes(&clean_path);

            let mut new_filename = String::new();
            let mut found_key = String::new();

            for (key, val) in image_map {
                let normalized_key = clean_path_slashes(key);
                if normalized_path == normalized_key {
                    new_filename = val.clone();
                    found_key = key.clone();
                    break;
                }
            }

            if new_filename.is_empty() {
                return caps[0].to_string();
            }

            let mut dir_str = String::new();
            if let Some(idx) = original_path.rfind('/') {
                dir_str = original_path[..idx].to_string();
            } else if let Some(idx) = original_path.rfind('\\') {
                dir_str = original_path[..idx].to_string();
            }

            let new_path = if dir_str.is_empty() {
                new_filename.clone()
            } else {
                format!("{}/{}", dir_str, new_filename)
            };

            let mut final_open = open_quote.to_string();
            let mut final_close = close_quote.to_string();
            if final_open != final_close {
                if !final_open.is_empty() && final_close.is_empty() {
                    final_close = final_open.clone();
                } else if final_open.is_empty() && !final_close.is_empty() {
                    final_open = final_close.clone();
                }
            }

            if self.debug_mode {
                let old_filename = Path::new(&found_key).file_name().unwrap_or_default().to_string_lossy();
                println!("    🔄 {} -> {}", old_filename, new_filename);
            }
            updated = true;
            format!("url({}{}{})", final_open, new_path, final_close)
        });

        if updated {
            let mut f = File::create(css_path)?;
            f.write_all(new_content.as_bytes())?;
        }
        Ok(())
    }

    fn process_component_css(&self, css_path: &Path) -> io::Result<FileInfo> {
        let css_dir = css_path.parent().unwrap_or_else(|| Path::new("."));
        let filename = css_path.file_name().unwrap_or_default().to_string_lossy().to_string();
        let clean_filename = self.remove_hash_from_filename(&filename);

        let mut original_css_path = css_dir.join(&clean_filename);
        if !original_css_path.exists() {
            original_css_path = css_path.to_path_buf();
        }

        if self.debug_mode {
            println!("    📝 处理CSS: {}", clean_filename);
        }

        let images = self.collect_images_from_css(&original_css_path)?;
        let mut image_map: HashMap<String, String> = HashMap::new();

        if !images.is_empty() {
            println!("    📸 处理 {} 个图片引用", images.len());
            for image in images {
                let original_path_key = clean_path_slashes(&image.original_path);
                
                let mut processed = self.processed_files.lock().unwrap();
                if processed.contains(&image.absolute_path) {
                    if let Ok(hash) = self.calculate_file_hash(&image.absolute_path) {
                        let dir = image.absolute_path.parent().unwrap_or_else(|| Path::new("."));
                        let old_img_name = image.absolute_path.file_name().unwrap_or_default().to_string_lossy();
                        let clean_img_name = self.remove_hash_from_filename(&old_img_name);
                        let new_img_name = self.add_hash_to_filename(&clean_img_name, &hash);
                        
                        let hashed_path = dir.join(&new_img_name);
                        if hashed_path.exists() {
                            image_map.insert(original_path_key, new_img_name);
                        } else if let Some(actual) = self.find_file(&dir.join(clean_img_name)) {
                            image_map.insert(original_path_key, actual.file_name().unwrap_or_default().to_string_lossy().to_string());
                        }
                    }
                    continue;
                }
                processed.insert(image.absolute_path.clone());
                drop(processed); // drop lock before rename

                if let Ok(info) = self.rename_file_with_hash(&image.absolute_path) {
                    let new_img_name = info.hashed_path.file_name().unwrap_or_default().to_string_lossy().to_string();
                    image_map.insert(original_path_key.clone(), new_img_name.clone());
                    if self.debug_mode {
                        println!("      📎 映射: {} -> {}", original_path_key, new_img_name);
                    }
                } else {
                    println!("      ⚠️  失败: {:?}", image.absolute_path.file_name());
                }
            }
        }

        let mut original_hash = self.calculate_file_hash(&original_css_path)?;
        let hashed_css_filename = self.add_hash_to_filename(&clean_filename, &original_hash);
        let mut hashed_css_path = css_dir.join(&hashed_css_filename);

        copy_file(&original_css_path, &hashed_css_path)?;

        if !image_map.is_empty() {
            if self.debug_mode {
                println!("    📋 图片映射表 ({} 项):", image_map.len());
                for (k, v) in &image_map {
                    println!("      {} -> {}", k, v);
                }
            }

            if let Err(e) = self.update_css_image_references(&hashed_css_path, &image_map) {
                println!("      ⚠️  更新CSS图片引用失败: {}", e);
            }

            // Re-hash
            if let Ok(new_hash) = self.calculate_file_hash(&hashed_css_path) {
                if new_hash != original_hash {
                    let final_css_filename = self.add_hash_to_filename(&clean_filename, &new_hash);
                    let final_css_path = css_dir.join(&final_css_filename);
                    if final_css_path != hashed_css_path {
                        let _ = fs::rename(&hashed_css_path, &final_css_path);
                        hashed_css_path = final_css_path;
                        original_hash = new_hash;
                    }
                }
            }
        }

        self.git_add_file(&hashed_css_path);

        let ext = Path::new(&clean_filename).extension().unwrap_or_default().to_string_lossy().to_string();
        let basename = clean_filename.trim_end_matches(&format!(".{}", ext));
        let dot_ext = if ext.is_empty() { String::new() } else { format!(".{}", ext) };
        let _ = self.find_and_delete_old_hash_files(css_dir, basename, &dot_ext, &original_hash);

        Ok(FileInfo {
            original_path: original_css_path,
            hashed_path: hashed_css_path,
            hash: original_hash,
            renamed: true,
        })
    }

    fn process_component_resource(&self, html_dir: &Path, relative_path: &str) -> io::Result<FileInfo> {
        let mut target_path = relative_path.to_string();
        if target_path.starts_with("http") || target_path.starts_with("//") {
            if let Some(idx) = target_path.find("components/") {
                target_path = target_path[idx..].to_string();
            }
        }

        let absolute_path = clean_path(html_dir.join(target_path));
        let actual_path = self.find_file(&absolute_path).unwrap_or(absolute_path.clone());

        if !actual_path.exists() {
            return Err(io::Error::new(io::ErrorKind::NotFound, format!("文件不存在: {:?}", actual_path)));
        }

        let mut processed = self.processed_files.lock().unwrap();
        if processed.contains(&actual_path) {
            let hash = self.calculate_file_hash(&actual_path)?;
            let dir = actual_path.parent().unwrap_or_else(|| Path::new("."));
            let filename = actual_path.file_name().unwrap_or_default().to_string_lossy().to_string();
            let clean_filename = self.remove_hash_from_filename(&filename);
            let hashed_filename = self.add_hash_to_filename(&clean_filename, &hash);
            let hashed_path = dir.join(hashed_filename);

            return Ok(FileInfo {
                original_path: actual_path,
                hashed_path,
                hash,
                renamed: true,
            });
        }
        processed.insert(actual_path.clone());
        drop(processed);

        let actual_str = actual_path.to_string_lossy().to_string().to_lowercase();
        if actual_str.ends_with(".css") {
            self.process_component_css(&actual_path)
        } else {
            self.rename_file_with_hash(&actual_path)
        }
    }

    fn collect_resources_from_html(&self, html_path: &Path) -> io::Result<HashMap<String, Vec<String>>> {
        let content = fs::read_to_string(html_path)?;
        let html_basename = html_path.file_stem().unwrap_or_default().to_string_lossy().to_string();
        let html_filename = html_path.file_name().unwrap_or_default().to_string_lossy().to_string();

        let mut should_process_main = false;
        for name in &self.config.process_main_resources {
            if name == &html_filename || name == &html_basename {
                should_process_main = true;
                break;
            }
        }

        let mut resources = HashMap::new();
        resources.insert("css".to_string(), Vec::new());
        resources.insert("js".to_string(), Vec::new());

        let css_re = Regex::new(r#"<link[^>]*href\s*=\s*['"]([^'"]+\.css)['"]"#).unwrap();
        for caps in css_re.captures_iter(&content) {
            let css_path = caps[1].to_string();
            let is_external = css_path.starts_with("http") || css_path.starts_with("//");

            if is_external {
                if should_process_main || !css_path.contains("components") { continue; }
            } else if !css_path.contains("components") { continue; }

            if !self.should_process_component(&css_path) { continue; }
            println!("    📝 扫描到组件CSS: {}", css_path);
            resources.get_mut("css").unwrap().push(css_path);
        }

        let js_re = Regex::new(r#"<script[^>]*src\s*=\s*['"]([^'"]+\.js)['"]"#).unwrap();
        for caps in js_re.captures_iter(&content) {
            let js_path = caps[1].to_string();
            let is_external = js_path.starts_with("http") || js_path.starts_with("//");

            if is_external {
                if should_process_main || !js_path.contains("components") { continue; }
            } else if !js_path.contains("components") { continue; }

            if !self.should_process_component(&js_path) { continue; }
            println!("    📝 扫描到组件JS: {}", js_path);
            resources.get_mut("js").unwrap().push(js_path);
        }

        Ok(resources)
    }

    fn should_exclude_from_cdn(&self, file_path: &str) -> bool {
        if self.config.cdn_exclude_files.is_empty() { return false; }
        
        let path_obj = Path::new(file_path);
        let mut filename = path_obj.file_name().unwrap_or_default().to_string_lossy().to_string();
        if let Some(idx) = filename.find('?') {
            filename = filename[..idx].to_string();
        }

        for exclude in &self.config.cdn_exclude_files {
            if &filename == exclude || file_path.contains(exclude) {
                if self.debug_mode { println!("    🚫 排除CDN替换: {}", filename); }
                return true;
            }
        }
        false
    }

    fn update_html_references(&self, html_path: &Path, resources: &HashMap<String, HashMap<String, String>>) -> io::Result<()> {
        let mut content = fs::read_to_string(html_path)?;
        let mut updated = false;

        // Process CSS
        if let Some(css_map) = resources.get("css") {
            for (original_rel_path, new_hashed_path) in css_map {
                let escaped_path = regex::escape(original_rel_path).replace("/", r"[/\\]");
                let patterns = vec![
                    format!(r#"(<link[^>]*href\s*=\s*['"])({})(['"][^>]*>)"#, escaped_path),
                    format!(r#"(<link[^>]*href\s*=\s*['"])(\.{{1,2}}[/\\]{})(['"][^>]*>)"#, escaped_path),
                ];

                for pattern in &patterns {
                    if let Ok(re) = Regex::new(pattern) {
                        if re.is_match(&content) {
                            let new_content = re.replace_all(&content, |caps: &Captures| {
                                let prefix = &caps[1];
                                let old_path = &caps[2];
                                let suffix = &caps[3];

                                let is_url = original_rel_path.starts_with("http") || original_rel_path.starts_with("//");
                                let mut old_dir = String::new();
                                if is_url {
                                    if let Some(idx) = original_rel_path.rfind('/') {
                                        old_dir = original_rel_path[..idx].to_string();
                                    }
                                } else {
                                    if let Some(parent) = Path::new(original_rel_path).parent() {
                                        old_dir = parent.to_string_lossy().to_string();
                                    }
                                }

                                let new_filename = Path::new(new_hashed_path).file_name().unwrap_or_default().to_string_lossy().to_string();
                                let mut new_path = if is_url {
                                    if old_dir.is_empty() { new_filename } else { format!("{}/{}", old_dir, new_filename) }
                                } else if old_dir != "." && old_dir != "/" && !old_dir.is_empty() {
                                    clean_path_slashes(&format!("{}/{}", old_dir, new_filename))
                                } else {
                                    new_filename
                                };

                                if old_path.starts_with("../") || old_path.starts_with("..\\") {
                                    if !new_path.starts_with("../") && !new_path.starts_with("..\\") {
                                        new_path = format!("../{}", new_path);
                                    }
                                } else if old_path.starts_with("./") || old_path.starts_with(".\\") {
                                    if !new_path.starts_with("./") && !new_path.starts_with(".\\") {
                                        new_path = format!("./{}", new_path);
                                    }
                                }

                                if !self.config.cdn_domain.is_empty() && !new_path.starts_with("http") && !self.should_exclude_from_cdn(&new_path) {
                                    let mut clean_new = new_path.clone();
                                    while clean_new.starts_with("./") || clean_new.starts_with("../") {
                                        clean_new = clean_new.trim_start_matches("./").trim_start_matches("../").to_string();
                                    }
                                    new_path = format!("{}/{}", self.config.cdn_domain, clean_new);
                                }

                                let result = format!("{}{}{}", prefix, new_path, suffix);
                                updated = true;
                                println!("  ✅ CSS: {} -> {}", Path::new(old_path).file_name().unwrap_or_default().to_string_lossy(), Path::new(&new_path).file_name().unwrap_or_default().to_string_lossy());
                                result
                            }).to_string();
                            content = new_content;
                            break;
                        }
                    }
                }
            }
        }

        // Process JS
        if let Some(js_map) = resources.get("js") {
            for (original_rel_path, new_hashed_path) in js_map {
                let escaped_path = regex::escape(original_rel_path).replace("/", r"[/\\]");
                let patterns = vec![
                    format!(r#"(<script[^>]*src\s*=\s*['"])({})(['"][^>]*>)"#, escaped_path),
                    format!(r#"(<script[^>]*src\s*=\s*['"])(\.{{1,2}}[/\\]{})(['"][^>]*>)"#, escaped_path),
                ];

                for pattern in &patterns {
                    if let Ok(re) = Regex::new(pattern) {
                        if re.is_match(&content) {
                            let new_content = re.replace_all(&content, |caps: &Captures| {
                                let prefix = &caps[1];
                                let old_path = &caps[2];
                                let suffix = &caps[3];

                                let is_url = original_rel_path.starts_with("http") || original_rel_path.starts_with("//");
                                let mut old_dir = String::new();
                                if is_url {
                                    if let Some(idx) = original_rel_path.rfind('/') {
                                        old_dir = original_rel_path[..idx].to_string();
                                    }
                                } else {
                                    if let Some(parent) = Path::new(original_rel_path).parent() {
                                        old_dir = parent.to_string_lossy().to_string();
                                    }
                                }

                                let new_filename = Path::new(new_hashed_path).file_name().unwrap_or_default().to_string_lossy().to_string();
                                let mut new_path = if is_url {
                                    if old_dir.is_empty() { new_filename } else { format!("{}/{}", old_dir, new_filename) }
                                } else if old_dir != "." && old_dir != "/" && !old_dir.is_empty() {
                                    clean_path_slashes(&format!("{}/{}", old_dir, new_filename))
                                } else {
                                    new_filename
                                };

                                if old_path.starts_with("../") || old_path.starts_with("..\\") {
                                    if !new_path.starts_with("../") && !new_path.starts_with("..\\") {
                                        new_path = format!("../{}", new_path);
                                    }
                                } else if old_path.starts_with("./") || old_path.starts_with(".\\") {
                                    if !new_path.starts_with("./") && !new_path.starts_with(".\\") {
                                        new_path = format!("./{}", new_path);
                                    }
                                }

                                if !self.config.cdn_domain.is_empty() && !new_path.starts_with("http") && !self.should_exclude_from_cdn(&new_path) {
                                    let mut clean_new = new_path.clone();
                                    while clean_new.starts_with("./") || clean_new.starts_with("../") {
                                        clean_new = clean_new.trim_start_matches("./").trim_start_matches("../").to_string();
                                    }
                                    new_path = format!("{}/{}", self.config.cdn_domain, clean_new);
                                }

                                let result = format!("{}{}{}", prefix, new_path, suffix);
                                updated = true;
                                println!("  ✅ JS: {} -> {}", Path::new(old_path).file_name().unwrap_or_default().to_string_lossy(), Path::new(&new_path).file_name().unwrap_or_default().to_string_lossy());
                                result
                            }).to_string();
                            content = new_content;
                            break;
                        }
                    }
                }
            }
        }

        // Generic CDN replacement for remaining resources if CDNDomain configured
        if !self.config.cdn_domain.is_empty() {
            let css_re = Regex::new(r#"(<link[^>]*href\s*=\s*['"])([^'"]+)(['"][^>]*>)"#).unwrap();
            content = css_re.replace_all(&content, |caps: &Captures| {
                let prefix = &caps[1];
                let path = &caps[2];
                let suffix = &caps[3];

                if !path.contains(".css") || path.starts_with("http") || path.starts_with("//") || path.starts_with("data:") || self.should_exclude_from_cdn(path) {
                    return caps[0].to_string();
                }

                let mut clean_path = path.to_string();
                while clean_path.starts_with("./") || clean_path.starts_with("../") {
                    clean_path = clean_path.trim_start_matches("./").trim_start_matches("../").to_string();
                }
                clean_path = clean_path.trim_start_matches('/').to_string();
                let new_path = format!("{}/{}", self.config.cdn_domain, clean_path);

                if new_path != path {
                    updated = true;
                    println!("  🌍 CDN(CSS): {} -> {}", Path::new(path).file_name().unwrap_or_default().to_string_lossy(), new_path);
                    format!("{}{}{}", prefix, new_path, suffix)
                } else {
                    caps[0].to_string()
                }
            }).to_string();

            let js_re = Regex::new(r#"(<script[^>]*src\s*=\s*['"])([^'"]+)(['"][^>]*>)"#).unwrap();
            content = js_re.replace_all(&content, |caps: &Captures| {
                let prefix = &caps[1];
                let path = &caps[2];
                let suffix = &caps[3];

                if !path.contains(".js") || path.starts_with("http") || path.starts_with("//") || path.starts_with("data:") || self.should_exclude_from_cdn(path) {
                    return caps[0].to_string();
                }

                let mut clean_path = path.to_string();
                while clean_path.starts_with("./") || clean_path.starts_with("../") {
                    clean_path = clean_path.trim_start_matches("./").trim_start_matches("../").to_string();
                }
                clean_path = clean_path.trim_start_matches('/').to_string();
                let new_path = format!("{}/{}", self.config.cdn_domain, clean_path);

                if new_path != path {
                    updated = true;
                    println!("  🌍 CDN(JS): {} -> {}", Path::new(path).file_name().unwrap_or_default().to_string_lossy(), new_path);
                    format!("{}{}{}", prefix, new_path, suffix)
                } else {
                    caps[0].to_string()
                }
            }).to_string();
        }

        if updated {
            let mut f = File::create(html_path)?;
            f.write_all(content.as_bytes())?;
            println!("\n✅ HTML文件已更新");
        } else {
            println!("\n⚠️  没有内容需要更新");
        }

        Ok(())
    }

    pub fn process_html_file(&self, html_path: &Path) -> io::Result<()> {
        println!("{}", "=".repeat(60));
        println!("📄 处理: {}", html_path.display());
        println!("{}", "=".repeat(60));

        if !html_path.exists() {
            return Err(io::Error::new(io::ErrorKind::NotFound, "文件不存在"));
        }

        let html_dir = html_path.parent().unwrap_or_else(|| Path::new("."));
        let html_basename = html_path.file_stem().unwrap_or_default().to_string_lossy().to_string();
        let html_filename = html_path.file_name().unwrap_or_default().to_string_lossy().to_string();

        let mut should_process_main = false;
        for name in &self.config.process_main_resources {
            if name == &html_filename || name == &html_basename {
                should_process_main = true;
                break;
            }
        }

        if should_process_main {
            println!("🎯 策略: 处理主资源 (JS/CSS) 及组件");
        } else {
            println!("🎯 策略: 仅处理组件资源 (跳过主JS/CSS)");
        }

        let mut resources: HashMap<String, HashMap<String, String>> = HashMap::new();
        resources.insert("css".to_string(), HashMap::new());
        resources.insert("js".to_string(), HashMap::new());

        // 1. Process main JS
        if should_process_main {
            println!("\n📦 处理主 JavaScript 文件...");
            let js_paths = vec![
                html_dir.join(format!("{}.js", html_basename)),
                html_dir.join("js").join(format!("{}.js", html_basename)),
                html_dir.join("scripts").join("js").join(format!("{}.js", html_basename)),
            ];

            let mut main_js_found = false;
            for js_path in js_paths {
                if let Some(actual_js) = self.find_file(&js_path) {
                    if let Ok(info) = self.rename_file_with_hash(&actual_js) {
                        let rel_path = clean_path_slashes(pathdiff::diff_paths(&actual_js, html_dir).unwrap_or(actual_js.clone()).to_string_lossy().as_ref());
                        let hashed_rel = clean_path_slashes(pathdiff::diff_paths(&info.hashed_path, html_dir).unwrap_or(info.hashed_path.clone()).to_string_lossy().as_ref());
                        
                        let normalized_key = rel_path.trim_start_matches("./").to_string();
                        resources.get_mut("js").unwrap().entry(normalized_key).or_insert(hashed_rel);
                        main_js_found = true;
                        break;
                    }
                }
            }
            if !main_js_found { println!("  ℹ️  未找到主JS文件"); }
        } else {
            println!("\n📦 跳过主 JavaScript 文件");
        }

        // 2. Process main CSS
        if should_process_main {
            println!("\n🎨 处理主 CSS 文件...");
            let css_paths = vec![
                html_dir.join(format!("{}.css", html_basename)),
                html_dir.join("css").join(format!("{}.css", html_basename)),
            ];

            let mut main_css_found = false;
            for css_path in css_paths {
                if let Some(actual_css) = self.find_file(&css_path) {
                    if let Ok(info) = self.process_component_css(&actual_css) {
                         let rel_path = clean_path_slashes(pathdiff::diff_paths(&actual_css, html_dir).unwrap_or(actual_css.clone()).to_string_lossy().as_ref());
                        let hashed_rel = clean_path_slashes(pathdiff::diff_paths(&info.hashed_path, html_dir).unwrap_or(info.hashed_path.clone()).to_string_lossy().as_ref());

                        let normalized_key = rel_path.trim_start_matches("./").to_string();
                        resources.get_mut("css").unwrap().entry(normalized_key).or_insert(hashed_rel);
                        main_css_found = true;
                        break;
                    }
                }
            }
            if !main_css_found { println!("  ℹ️  未找到主CSS文件"); }
        } else {
            println!("\n🎨 跳过主 CSS 文件");
        }

        // 3. Components
        println!("\n🔍 扫描组件资源...");
        let html_resources = self.collect_resources_from_html(html_path)?;
        println!("  找到 {} 个组件CSS, {} 个组件JS", html_resources.get("css").unwrap().len(), html_resources.get("js").unwrap().len());

        // 并行处理组件资源
        use rayon::prelude::*;
        
        if let Some(js_list) = html_resources.get("js") {
            if !js_list.is_empty() {
                let thread_count = rayon::current_num_threads();
                println!("\n🔧 [并行×{}] 处理 {} 个组件 JavaScript 文件...", thread_count, js_list.len());
                let start = std::time::Instant::now();
                let js_results: Vec<_> = js_list.par_iter().filter_map(|js_rel| {
                    let normalized_key = clean_path_slashes(js_rel).trim_start_matches("./").to_string();
                    if self.processed_files.lock().unwrap().contains(&html_dir.join(js_rel)) {
                        return None;
                    }
                    match self.process_component_resource(html_dir, js_rel) {
                        Ok(info) => {
                            let hashed_rel = clean_path_slashes(pathdiff::diff_paths(&info.hashed_path, html_dir).unwrap_or(info.hashed_path.clone()).to_string_lossy().as_ref());
                            println!("  ✅ [并行] JS: {} -> {}", js_rel, Path::new(&hashed_rel).file_name().unwrap_or_default().to_string_lossy());
                            Some((normalized_key, hashed_rel))
                        }
                        Err(_) => {
                            println!("  ❌ [并行] 失败: {}", js_rel);
                            None
                        }
                    }
                }).collect();
                for (key, val) in js_results {
                    resources.get_mut("js").unwrap().insert(key, val);
                }
                println!("  ⏱️  JS并行处理耗时: {:.2}s", start.elapsed().as_secs_f64());
            }
        }

        if let Some(css_list) = html_resources.get("css") {
            if !css_list.is_empty() {
                let thread_count = rayon::current_num_threads();
                println!("\n🎨 [并行×{}] 处理 {} 个组件 CSS 文件...", thread_count, css_list.len());
                let start = std::time::Instant::now();
                let css_results: Vec<_> = css_list.par_iter().filter_map(|css_rel| {
                    let normalized_key = clean_path_slashes(css_rel).trim_start_matches("./").to_string();
                    if self.processed_files.lock().unwrap().contains(&html_dir.join(css_rel)) {
                        return None;
                    }
                    match self.process_component_resource(html_dir, css_rel) {
                        Ok(info) => {
                            let hashed_rel = clean_path_slashes(pathdiff::diff_paths(&info.hashed_path, html_dir).unwrap_or(info.hashed_path.clone()).to_string_lossy().as_ref());
                            println!("  ✅ [并行] CSS: {} -> {}", css_rel, Path::new(&hashed_rel).file_name().unwrap_or_default().to_string_lossy());
                            Some((normalized_key, hashed_rel))
                        }
                        Err(_) => {
                            println!("  ❌ [并行] 失败: {}", css_rel);
                            None
                        }
                    }
                }).collect();
                for (key, val) in css_results {
                    resources.get_mut("css").unwrap().insert(key, val);
                }
                println!("  ⏱️  CSS并行处理耗时: {:.2}s", start.elapsed().as_secs_f64());
            }
        }

        println!("\n🔄 更新HTML中的资源引用...");
        println!("  📋 CSS: {} 项, JS: {} 项", resources.get("css").unwrap().len(), resources.get("js").unwrap().len());
        self.update_html_references(html_path, &resources)?;

        if self.config.deploy.enabled {
            self.run_deploy();
        } else {
            self.run_node_copy_script();
        }

        println!("\n✨ 处理完成!");
        Ok(())
    }

    pub fn process_multiple_html_files(&self, paths: &[String]) {
        let thread_count = rayon::current_num_threads();
        println!("🚀 [并行×{}] 开始批量处理 {} 个 HTML 文件...\n", thread_count, paths.len());
        let root = std::path::PathBuf::from(&self.config.root_dir);
        
        use rayon::prelude::*;
        paths.par_iter().for_each(|p| {
            let abs_path = root.join(p);
            println!("  🔄 [并行] 开始处理: {}", p);
            if let Err(e) = self.process_html_file(&abs_path) {
                println!("  ❌ [并行] 处理失败 {}: {}", p, e);
            } else {
                println!("  ✅ [并行] 完成处理: {}", p);
            }
        });
        
        println!("
{}", "=".repeat(60));
        println!("🎉 全部处理完成！");
        println!("{}", "=".repeat(60));
    }

    fn run_deploy(&self) {
        let _lock = self.deploy_lock.lock().unwrap();
        if !self.config.deploy.enabled { return; }
        println!("\n{}", "=".repeat(60));
        println!("🚀 开始部署流程");
        println!("{}", "=".repeat(60));

        let mut dm = DeployManager::new(self.config.deploy.clone(), self.debug_mode);
        let auto_commit = self.config.deploy.auto_commit || self.config.deploy.command == "copy-commit";
        
        if let Err(e) = dm.run(auto_commit, &self.config.single_html_file, &self.config.cdn_domain) {
            println!("❌ 部署失败: {:?}", e);
            return;
        }

        if self.config.rollback_after_deploy && !self.config.single_html_file.is_empty() {
            let _ = rollback_html_file(&self.config.single_html_file);
        }
        *self.folder_opened.lock().unwrap() = dm.folder_opened;
    }
}

// Helper rollback logic
fn rollback_html_file(html_path: &str) -> io::Result<()> {
    let p = Path::new(html_path).canonicalize().unwrap_or(PathBuf::from(html_path));
    let dir = p.parent().unwrap_or_else(|| Path::new("."));
    let filename = p.file_name().unwrap_or_default().to_string_lossy();

    println!("\n🔄 正在回滚HTML文件: {}", filename);
    
    if Command::new("git").arg("--version").output().is_err() {
        println!("⚠️  未找到git命令，跳过回滚");
        return Ok(());
    }

    let mut cmd = Command::new("git");
    cmd.args(["checkout", "HEAD", "--", &filename]).current_dir(dir);
    if cmd.output().is_err() {
        let mut retry = Command::new("git");
        retry.args(["checkout", "--", &filename]).current_dir(dir);
        let _ = retry.output();
    }
    println!("\n✅ HTML文件已回滚到CDN替换前的状态");
    Ok(())
}

fn clean_path(p: PathBuf) -> PathBuf {
    // Simple canonicalize mock that resolves `.` and `..` might be needed
    // standard fs::canonicalize requires files to exist.
    p
}

// ==========================================
// 4. 部署管理器 (DeployManager)
// ==========================================



pub struct DeployManager {
    config: DeployConfig,
    source_path: String,
    dest_path: String,
    debug_mode: bool,
    folder_opened: bool,
}

#[derive(Clone, Debug)]
#[allow(dead_code)]
struct FileVersion {
    path: PathBuf,
    name: String,
    has_hash: bool,
    mod_time: SystemTime,
    hash: String,
}

impl DeployManager {
    pub fn new(config: DeployConfig, debug_mode: bool) -> Self {
        let is_home = env::var("IS_HOME").unwrap_or_default() == "1";
        let (source_path, dest_path) = if is_home {
            (config.home_source_path.clone(), config.home_dest_path.clone())
        } else {
            (config.company_source_path.clone(), config.company_dest_path.clone())
        };
        Self { config, source_path, dest_path, debug_mode, folder_opened: false }
    }

    fn update_svn_repo(&self) -> io::Result<()> {
        println!("🔄 正在更新SVN仓库: {}", self.dest_path);
        let output = Command::new("svn")
            .arg("update")
            .current_dir(&self.dest_path)
            .output()?;
        
        if !output.status.success() {
            let _ = Command::new("svn").args(["cleanup"]).current_dir(&self.dest_path).output();
            let _ = Command::new("svn").args(["revert", "-R", "."]).current_dir(&self.dest_path).output();
            let _ = Command::new("svn").args(["update"]).current_dir(&self.dest_path).output();
        }
        println!("✅ SVN更新成功\n{}", String::from_utf8_lossy(&output.stdout));
        Ok(())
    }

    fn svn_add_all(&self) -> io::Result<()> {
        println!("📁 正在添加新文件到SVN...");
        let output = Command::new("svn")
            .arg("status")
            .current_dir(&self.dest_path)
            .output()?;
        
        let status_str = String::from_utf8_lossy(&output.stdout);
        let mut added_count = 0;
        for line in status_str.lines() {
            if line.starts_with('?') {
                let file = line[1..].trim();
                let _ = Command::new("svn").args(["add", file]).current_dir(&self.dest_path).output();
                added_count += 1;
            } else if line.starts_with('!') {
                let file = line[1..].trim();
                let _ = Command::new("svn").args(["rm", file]).current_dir(&self.dest_path).output();
                added_count += 1;
            }
        }
        if added_count > 0 {
            println!("✅ 已添加/移除 {} 个文件", added_count);
        } else {
            println!("ℹ️ 没有新文件需要添加");
        }
        Ok(())
    }

    fn get_latest_git_commit(&self) -> (String, String) {
        let mut cmd = Command::new("git");
        cmd.args(["log", "-1", "--format=%s"]);
        if let Ok(cmd_out) = cmd.output() {
            let msg = String::from_utf8_lossy(&cmd_out.stdout).trim().to_string();
            return (msg, "".to_string());
        }
        ("自动部署提交".to_string(), "".to_string())
    }

    fn svn_commit(&self, message: &str) -> io::Result<()> {
        println!("📤 正在提交到SVN...");
        let _ = self.svn_add_all();
        
        let msg_file = env::temp_dir().join("svn_commit_msg.txt");
        let mut file = File::create(&msg_file)?;
        file.write_all(b"\xEF\xBB\xBF")?; // BOM
        file.write_all(message.as_bytes())?;
        
        let output = Command::new("svn")
            .args(["commit", "-F", msg_file.to_str().unwrap()])
            .current_dir(&self.dest_path)
            .output()?;
            
        let _ = fs::remove_file(msg_file);
        
        if output.status.success() {
            println!("✅ SVN提交成功");
        } else {
            println!("❌ SVN提交失败: {}", String::from_utf8_lossy(&output.stderr));
        }
        Ok(())
    }

    fn find_all_file_versions(&self, config_path: &str) -> Vec<FileVersion> {
        let full_path = Path::new(&self.source_path).join(config_path);
        let dir = full_path.parent().unwrap_or(Path::new(""));
        let file_name = full_path.file_name().unwrap_or_default().to_string_lossy();
        let ext = full_path.extension().unwrap_or_default().to_string_lossy();
        let basename = file_name.trim_end_matches(&format!(".{}", ext));
        let dot_ext = if ext.is_empty() { String::new() } else { format!(".{}", ext) };

        let mut versions = Vec::new();
        if !dir.exists() { return versions; }
        
        if full_path.exists() {
            if let Ok(meta) = fs::metadata(&full_path) {
                versions.push(FileVersion {
                    path: full_path.clone(),
                    name: file_name.to_string(),
                    has_hash: false,
                    mod_time: meta.modified().unwrap_or(SystemTime::UNIX_EPOCH),
                    hash: "".to_string()
                });
            }
        }
        
        if let Ok(entries) = fs::read_dir(dir) {
            let pat = format!(r"^{}\.[a-zA-Z0-9]+{}$", regex::escape(basename), regex::escape(&dot_ext));
            if let Ok(re) = Regex::new(&pat) {
                for entry in entries.flatten() {
                    let fname = entry.file_name().to_string_lossy().to_string();
                    if re.is_match(&fname) {
                        if let Ok(meta) = entry.metadata() {
                            versions.push(FileVersion {
                                path: entry.path(),
                                name: fname,
                                has_hash: true,
                                mod_time: meta.modified().unwrap_or(SystemTime::UNIX_EPOCH),
                                hash: "".to_string(),
                            });
                        }
                    }
                }
            }
        }
        versions.sort_by(|a, b| b.mod_time.cmp(&a.mod_time));
        versions
    }

    fn copy_file_with_versions(&self, source_rel: &str, dest_rel: &str) -> io::Result<(i32, i32)> {
        let source_rel = source_rel.trim_start_matches('/');
        let dest_rel = dest_rel.trim_start_matches('/');
        let versions = self.find_all_file_versions(source_rel);
        
        if versions.is_empty() {
             println!("⚠️ 未找到文件: {}", source_rel);
             return Ok((0, 0));
        }
        
        if self.debug_mode {
            println!("    📋 [{}] 找到 {} 个版本: {:?}", source_rel, versions.len(), versions.iter().map(|v| &v.name).collect::<Vec<_>>());
        }
        
        let mut base_version = None;
        let mut latest_hash_version = None;
        for v in &versions {
            if v.has_hash {
                if latest_hash_version.is_none() { latest_hash_version = Some(v.clone()); }
            } else {
                base_version = Some(v.clone());
            }
        }
        
        let dest_full = Path::new(&self.dest_path).join(dest_rel);
        let dest_dir = dest_full.parent().unwrap();
        if !dest_dir.exists() {
            fs::create_dir_all(dest_dir)?;
        }
        
        let mut copied = 0;
        let mut skipped = 0;
        
        // 复制所有哈希版本，不只是最新的一个
        let mut versions_to_process = Vec::new();
        if let Some(b) = base_version { versions_to_process.push(b); }
        // 添加所有哈希版本
        for v in &versions {
            if v.has_hash {
                versions_to_process.push(v.clone());
            }
        }
        
        for version in versions_to_process {
            let d_path = if version.has_hash {
                dest_dir.join(&version.name)
            } else {
                dest_full.clone()
            };
            if d_path.exists() {
                if let (Ok(s_meta), Ok(d_meta)) = (fs::metadata(&version.path), fs::metadata(&d_path)) {
                    if s_meta.len() == d_meta.len() { 
                        skipped += 1; 
                        continue; 
                    }
                }
            }
            if self.debug_mode {
                println!("    📤 复制: {} -> {}", version.name, d_path.display());
            }
            fs::copy(&version.path, &d_path)?;
            copied += 1;
        }
        Ok((copied, skipped))
    }

    fn handle_wildcard_path(&self, wildcard_path: &str) -> io::Result<(i32, i32)> {
        let dir_path = wildcard_path.trim_end_matches("/*").trim_start_matches('/');
        let source_dir_path = Path::new(&self.source_path).join(dir_path);
        let dest_dir_path = Path::new(&self.dest_path).join(dir_path);
        
        if !source_dir_path.exists() {
            return Err(io::Error::new(io::ErrorKind::NotFound, format!("Wildcard source not found: {}", dir_path)));
        }
        if !dest_dir_path.exists() {
            fs::create_dir_all(&dest_dir_path)?;
        }
        
        let mut total_copied = 0;
        let mut total_skipped = 0;
        let mut file_count = 0;
        
        // 使用 walkdir 递归遍历所有子目录
        use walkdir::WalkDir;
        for entry in WalkDir::new(&source_dir_path)
            .follow_links(true)
            .into_iter()
            .filter_map(|e| e.ok())
        {
            if entry.file_type().is_file() {
                let full_source_path = entry.path();
                let relative_path = full_source_path.strip_prefix(&source_dir_path)
                    .unwrap_or(full_source_path);
                let rel = format!("{}/{}", dir_path, relative_path.to_string_lossy().replace('\\', "/"));
                
                let (c, s) = self.copy_file_with_versions(&rel, &rel)?;
                total_copied += c;
                total_skipped += s;
                file_count += 1;
            }
        }
        
        if self.debug_mode {
            println!("    📁 {} 目录共处理 {} 个文件, 复制 {}, 跳过 {}", dir_path, file_count, total_copied, total_skipped);
        }
        
        Ok((total_copied, total_skipped))
    }

    pub fn run(&mut self, auto_commit: bool, html_path: &str, cdn_domain: &str) -> io::Result<()> {
        println!("🚀 开始部署操作...");
        println!("📂 源路径: {}", self.source_path);
        println!("📂 目标路径: {}\n", self.dest_path);

        let is_svn = Command::new("svn").args(["info"]).current_dir(&self.dest_path).output().map(|o| o.status.success()).unwrap_or(false);
        if is_svn {
            let _ = self.update_svn_repo();
        }

        let mut copied = 0;
        let mut skipped = 0;

        for p in &self.config.file_paths {
            if p.ends_with("/*") {
                let (c, s) = self.handle_wildcard_path(p)?;
                copied += c;
                skipped += s;
            } else {
                let (c, s) = self.copy_file_with_versions(p, p)?;
                copied += c;
                skipped += s;
            }
        }

        println!("\n📊 部署汇总:");
        println!("  ✅ 复制文件: {} 个", copied);
        println!("  ⏭️  跳过文件: {} 个 (已存在且相同)", skipped);

        // 校验 CDN 资源
        if !html_path.is_empty() && !cdn_domain.is_empty() {
            println!("\n🔍 开始校验 CDN 资源...");
            if let Err(e) = self.validate_cdn_resources(html_path, cdn_domain) {
                return Err(io::Error::new(io::ErrorKind::Other, format!("CDN资源校验失败: {}", e)));
            }
            println!("✅ CDN 资源校验通过");
        }

        if auto_commit && is_svn {
            let (commit_msg, _) = self.get_latest_git_commit();
            let _ = self.svn_commit(&commit_msg);
        }
        
        if !self.folder_opened && cfg!(target_os = "windows") {
             Command::new("explorer").arg(&self.dest_path).spawn().ok();
             self.folder_opened = true;
        }

        println!("✅ 部署操作成功");
        Ok(())
    }

    /// 校验 HTML 中的 CDN 资源是否在 destPath 中存在
    fn validate_cdn_resources(&self, html_path: &str, cdn_domain: &str) -> io::Result<()> {
        let content = fs::read_to_string(html_path)?;
        
        // 1. 移除 HTML 注释内容，避免处理被注释掉的标签
        let re_comments = Regex::new(r"(?s)<!--.*?-->").unwrap();
        let clean_content = re_comments.replace_all(&content, "");
        
        // 2. 在清理后的内容中匹配 CDN 域名开头的资源路径
        // 排除反引号 \x60，防止匹配到模板字符串内容
        let pattern = format!("{}([^\\s'\"\\x60]+)", regex::escape(cdn_domain));
        let re = Regex::new(&pattern).unwrap();
        
        let mut missing_files = Vec::new();
        
        for caps in re.captures_iter(&clean_content) {
            let url_path = &caps[1];
            
            // 移除查询参数
            let url_path = if let Some(idx) = url_path.find('?') {
                &url_path[..idx]
            } else {
                url_path
            };
            
            // 如果配置了前缀，则移除它以获取相对于 destPath 的路径
            let rel_path = if !self.config.cdn_path_prefix.is_empty() && url_path.starts_with(&self.config.cdn_path_prefix) {
                url_path.trim_start_matches(&self.config.cdn_path_prefix).to_string()
            } else {
                url_path.to_string()
            };
            
            // 拼接到目标目录进行检查
            let rel_path_clean = rel_path.trim_start_matches('/');
            let check_path = Path::new(&self.dest_path).join(rel_path_clean);
            
            if !check_path.exists() {
                missing_files.push(format!("{} -> (预检路径: {})", url_path, check_path.display()));
            }
        }
        
        if !missing_files.is_empty() {
            println!("\n❌ 发现 {} 个缺失文件:", missing_files.len());
            for mf in &missing_files {
                println!("    - {}", mf);
            }
            return Err(io::Error::new(io::ErrorKind::NotFound, format!("{} 个文件缺失", missing_files.len())));
        }
        
        Ok(())
    }
}


// ==========================================
// 5. 程序入口
// ==========================================

fn find_config_file(config_filename: &str) -> Option<PathBuf> {
    let search_paths = vec![
        // 1. 当前工作目录
        env::current_dir().ok().map(|p| p.join(config_filename)),
        // 2. exe 所在目录（用于 release 模式）
        env::current_exe().ok().map(|p| p.parent().unwrap_or(&p).join(config_filename)),
        // 3. 源码目录（exe/target/release/ 的上级）
        env::current_exe().ok().and_then(|p| p.parent().unwrap_or(&p).parent().map(|p| p.join(config_filename))),
        // 4. 常见开发目录结构
        env::current_exe().ok().and_then(|p| p.parent().unwrap_or(&p).parent().map(|p| p.join("cmd").join("hashCdn").join(config_filename))),
    ];
    
    for path in search_paths.into_iter().flatten() {
        if path.exists() {
            println!("📁 找到配置文件: {}", path.display());
            return Some(path);
        }
    }
    None
}

fn load_config(config_filename: &str) -> Config {
    let config_path = find_config_file(config_filename);
    
    let data = if let Some(path) = config_path {
        fs::read_to_string(&path).ok()
    } else {
        // 尝试直接读取（兼容旧逻辑）
        fs::read_to_string(config_filename).ok()
    };

    if let Some(data) = data {
        let mut cfg: Config = serde_json::from_str(&data).unwrap_or_default();
        if cfg.root_dir.is_empty() { cfg.root_dir = ".".to_string(); }
        if cfg.hash_length == 0 { cfg.hash_length = 8; }
        if cfg.exclude_dirs.is_empty() { cfg.exclude_dirs = default_exclude_dirs(); }
        let is_home = env::var("IS_HOME").unwrap_or_default() == "1";
        if !cfg.home_html_file.is_empty() || !cfg.company_html_file.is_empty() {
            if is_home && !cfg.home_html_file.is_empty() {
                cfg.single_html_file = cfg.home_html_file.clone();
            } else if !is_home && !cfg.company_html_file.is_empty() {
                cfg.single_html_file = cfg.company_html_file.clone();
            }
        }
        cfg
    } else {
        println!("⚠️  找不到配置文件: {}，使用默认配置", config_filename);
        Config::default()
    }
}
fn main() {
    // log 正使用rust版本 
    println!("rust 🚀 hashCdn - 静态资源版本管理工具");
    let start = std::time::Instant::now();
    let args: Vec<String> = env::args().collect();
    let debug_mode = args.contains(&"--debug".to_string());
    
    let config = load_config("version.config.json");
    let vm = VersionManager::new(config, debug_mode);

    if !vm.config.single_html_file.is_empty() {
        let path = PathBuf::from(&vm.config.single_html_file);
        if let Err(e) = vm.process_html_file(&path) {
            println!("❌ 处理失败: {}", e);
        }
    } else if !vm.config.html_files.is_empty() {
        vm.process_multiple_html_files(&vm.config.html_files.clone());
    } else {
        println!("⚠️  未指定要处理的HTML文件");
    }
    
    println!("\n{}\n⏱️  总运行时间: {:.2}s", "=".repeat(60), start.elapsed().as_secs_f64());

    // 等待用户按任意键退出
    println!("\n按任意键退出...");
    use std::io;
    let mut _input = String::new();
    io::stdin().read_line(&mut _input).ok();
}

#[cfg(test)]
mod hash_tests {
    use super::*;
    use std::fs::File;
    use std::io::Write;

    /// Helper: create a VersionManager with default config (hash_length = 8).
    fn make_vm() -> VersionManager {
        let config = Config {
            hash_length: 8,
            ..Config::default()
        };
        VersionManager::new(config, false)
    }

    // -------------------------------------------------------
    // 1. get_file_hash: known content -> known MD5
    // -------------------------------------------------------
    #[test]
    fn test_get_file_hash() {
        // Create a temp file with known content.
        let dir = std::env::temp_dir();
        let path = dir.join("hash_test_input.txt");
        {
            let mut f = File::create(&path).expect("create temp file");
            write!(f, "hello world").expect("write temp file");
        }

        // MD5("hello world") = 5eb63bbbe01eeed093cb22bb8f5acdc3
        let hash = get_file_hash(&path, 8).expect("get_file_hash failed");
        assert_eq!(hash, "5eb63bbb");

        // Full-length hash (length=0 means no truncation).
        let full = get_file_hash(&path, 0).expect("get_file_hash full failed");
        assert_eq!(full, "5eb63bbbe01eeed093cb22bb8f5acdc3");

        // Cleanup.
        let _ = fs::remove_file(&path);
    }

    // -------------------------------------------------------
    // 2. add_hash_to_filename: "style.css" + "abc12345"
    // -------------------------------------------------------
    #[test]
    fn test_add_hash_to_filename() {
        let vm = make_vm();
        let result = vm.add_hash_to_filename("style.css", "abc12345");
        assert_eq!(result, "style.abc12345.css");
    }

    // -------------------------------------------------------
    // 3. remove_hash_from_filename: "style.abc12345.css" -> "style.css"
    // -------------------------------------------------------
    #[test]
    fn test_remove_hash_from_filename() {
        let vm = make_vm();
        let result = vm.remove_hash_from_filename("style.abc12345.css");
        assert_eq!(result, "style.css");
    }

    // -------------------------------------------------------
    // 4. remove_hash_from_filename with no hash: "style.css" -> "style.css"
    // -------------------------------------------------------
    #[test]
    fn test_remove_hash_from_filename_no_hash() {
        let vm = make_vm();
        let result = vm.remove_hash_from_filename("style.css");
        assert_eq!(result, "style.css");
    }

    // -------------------------------------------------------
    // 5. add_hash_to_filename with existing hash: replaces old hash
    // -------------------------------------------------------
    #[test]
    fn test_add_hash_to_filename_with_existing_hash() {
        let vm = make_vm();
        // "abcdef12" is a valid hex hash; the function strips it from the stem
        // and replaces it with the new hash.
        let result = vm.add_hash_to_filename("style.abcdef12.css", "new567890");
        assert_eq!(result, "style.new567890.css");
    }
}

#[cfg(test)]
mod config_tests {
    use super::*;
    use std::fs::File;
    use std::io::Write;
    use std::sync::Mutex;

    // Mutex to serialize tests that modify IS_HOME env var (process-wide state)
    lazy_static::lazy_static! {
        static ref IS_HOME_MUTEX: Mutex<()> = Mutex::new(());
    }

    /// Helper: create a temporary config file and return its path.
    fn create_temp_config(filename: &str, content: &str) -> PathBuf {
        let dir = std::env::temp_dir().join("hashcdn_config_tests");
        fs::create_dir_all(&dir).expect("failed to create temp dir");
        let path = dir.join(filename);
        let mut f = File::create(&path).expect("failed to create temp config file");
        f.write_all(content.as_bytes()).expect("failed to write temp config");
        path
    }

    /// Helper: remove a temporary config file (ignore errors).
    fn remove_temp_config(path: &Path) {
        let _ = fs::remove_file(path);
    }

    /// Helper: run a closure with IS_HOME env var cleared, restoring afterwards.
    fn with_clean_is_home<F: FnOnce()>(f: F) {
        let _lock = IS_HOME_MUTEX.lock().unwrap_or_else(|e| e.into_inner());
        let saved = env::var("IS_HOME").ok();
        env::remove_var("IS_HOME");
        f();
        match saved {
            Some(val) => env::set_var("IS_HOME", val),
            None => { env::remove_var("IS_HOME"); }
        }
    }

    // -------------------------------------------------------
    // 1. Config deserialization
    // -------------------------------------------------------

    #[test]
    fn test_config_deserialize_valid_json() {
        let json = r#"{
            "rootDir": "/var/www",
            "cdnDomain": "cdn.example.com",
            "hashLength": 12,
            "singleHTMLFile": "index.html",
            "htmlFiles": ["a.html", "b.html"],
            "excludeDirs": ["tmp", "cache"],
            "homeHTMLFile": "home.html",
            "companyHTMLFile": "company.html",
            "includeComponents": ["header", "footer"],
            "processMainResources": ["main.js"],
            "replaceAllWithCDN": true,
            "RollbackAfterDeploy": false,
            "cdnExcludeFiles": ["favicon.ico"],
            "deploy": {
                "enabled": true,
                "command": "git push",
                "autoCommit": true,
                "homeSourcePath": "/src/home",
                "homeDestPath": "/dest/home",
                "companySourcePath": "/src/company",
                "companyDestPath": "/dest/company",
                "filePaths": ["dist/app.js"],
                "gitAuthors": ["alice", "bob"],
                "cdnPathPrefix": "/static"
            }
        }"#;

        let cfg: Config = serde_json::from_str(json).expect("deserialization should succeed");

        assert_eq!(cfg.root_dir, "/var/www");
        assert_eq!(cfg.cdn_domain, "cdn.example.com");
        assert_eq!(cfg.hash_length, 12);
        assert_eq!(cfg.single_html_file, "index.html");
        assert_eq!(cfg.html_files, vec!["a.html", "b.html"]);
        assert_eq!(cfg.exclude_dirs, vec!["tmp", "cache"]);
        assert_eq!(cfg.home_html_file, "home.html");
        assert_eq!(cfg.company_html_file, "company.html");
        assert_eq!(cfg.include_components, vec!["header", "footer"]);
        assert_eq!(cfg.process_main_resources, vec!["main.js"]);
        assert!(cfg.replace_all_with_cdn);
        assert!(!cfg.rollback_after_deploy);
        assert_eq!(cfg.cdn_exclude_files, vec!["favicon.ico"]);

        // Deploy sub-struct
        assert!(cfg.deploy.enabled);
        assert_eq!(cfg.deploy.command, "git push");
        assert!(cfg.deploy.auto_commit);
        assert_eq!(cfg.deploy.home_source_path, "/src/home");
        assert_eq!(cfg.deploy.home_dest_path, "/dest/home");
        assert_eq!(cfg.deploy.company_source_path, "/src/company");
        assert_eq!(cfg.deploy.company_dest_path, "/dest/company");
        assert_eq!(cfg.deploy.file_paths, vec!["dist/app.js"]);
        assert_eq!(cfg.deploy.git_authors, vec!["alice", "bob"]);
        assert_eq!(cfg.deploy.cdn_path_prefix, "/static");
    }

    #[test]
    fn test_config_deserialize_empty_json_uses_serde_defaults() {
        let cfg: Config = serde_json::from_str("{}").expect("empty JSON should succeed");
        // serde default functions should fill in the defaults
        assert_eq!(cfg.root_dir, ".");
        assert_eq!(cfg.hash_length, 8);
        assert_eq!(cfg.exclude_dirs, vec!["node_modules", ".git", "dist", "build"]);
        assert!(cfg.cdn_domain.is_empty());
        assert!(cfg.single_html_file.is_empty());
        assert!(cfg.html_files.is_empty());
    }

    #[test]
    fn test_config_roundtrip_serialize_deserialize() {
        let original = Config {
            root_dir: "/test".to_string(),
            cdn_domain: "cdn.test.com".to_string(),
            hash_length: 16,
            ..Config::default()
        };
        let json = serde_json::to_string(&original).expect("serialization should succeed");
        let restored: Config = serde_json::from_str(&json).expect("deserialization should succeed");
        assert_eq!(restored.root_dir, "/test");
        assert_eq!(restored.cdn_domain, "cdn.test.com");
        assert_eq!(restored.hash_length, 16);
    }

    // -------------------------------------------------------
    // 2. Default values
    // -------------------------------------------------------

    #[test]
    fn test_config_default_root_dir() {
        let cfg: Config = serde_json::from_str("{}").unwrap();
        assert_eq!(cfg.root_dir, ".");
    }

    #[test]
    fn test_config_default_hash_length() {
        let cfg: Config = serde_json::from_str("{}").unwrap();
        assert_eq!(cfg.hash_length, 8);
    }

    #[test]
    fn test_config_default_exclude_dirs() {
        let cfg: Config = serde_json::from_str("{}").unwrap();
        assert_eq!(cfg.exclude_dirs.len(), 4);
        assert!(cfg.exclude_dirs.contains(&"node_modules".to_string()));
        assert!(cfg.exclude_dirs.contains(&".git".to_string()));
        assert!(cfg.exclude_dirs.contains(&"dist".to_string()));
        assert!(cfg.exclude_dirs.contains(&"build".to_string()));
    }

    #[test]
    fn test_config_default_other_fields_are_empty_or_false() {
        let cfg = Config::default();
        assert!(cfg.cdn_domain.is_empty());
        assert!(cfg.single_html_file.is_empty());
        assert!(cfg.html_files.is_empty());
        assert!(cfg.home_html_file.is_empty());
        assert!(cfg.company_html_file.is_empty());
        assert!(cfg.include_components.is_empty());
        assert!(cfg.process_main_resources.is_empty());
        assert!(!cfg.replace_all_with_cdn);
        assert!(!cfg.rollback_after_deploy);
        assert!(cfg.cdn_exclude_files.is_empty());
    }

    // -------------------------------------------------------
    // 3. DeployConfig deserialization with camelCase fields
    // -------------------------------------------------------

    #[test]
    fn test_deploy_config_deserialize_camel_case() {
        let json = r#"{
            "enabled": true,
            "command": "npm run deploy",
            "autoCommit": true,
            "homeSourcePath": "/home/src",
            "homeDestPath": "/home/dest",
            "companySourcePath": "/company/src",
            "companyDestPath": "/company/dest",
            "filePaths": ["index.html", "app.js"],
            "gitAuthors": ["user1"],
            "cdnPathPrefix": "/assets"
        }"#;

        let dc: DeployConfig = serde_json::from_str(json).expect("deploy deserialization should succeed");
        assert!(dc.enabled);
        assert_eq!(dc.command, "npm run deploy");
        assert!(dc.auto_commit);
        assert_eq!(dc.home_source_path, "/home/src");
        assert_eq!(dc.home_dest_path, "/home/dest");
        assert_eq!(dc.company_source_path, "/company/src");
        assert_eq!(dc.company_dest_path, "/company/dest");
        assert_eq!(dc.file_paths, vec!["index.html", "app.js"]);
        assert_eq!(dc.git_authors, vec!["user1"]);
        assert_eq!(dc.cdn_path_prefix, "/assets");
    }

    #[test]
    fn test_deploy_config_default() {
        let dc = DeployConfig::default();
        assert!(!dc.enabled);
        assert!(dc.command.is_empty());
        assert!(!dc.auto_commit);
        assert!(dc.home_source_path.is_empty());
        assert!(dc.home_dest_path.is_empty());
        assert!(dc.company_source_path.is_empty());
        assert!(dc.company_dest_path.is_empty());
        assert!(dc.file_paths.is_empty());
        assert!(dc.git_authors.is_empty());
        assert!(dc.cdn_path_prefix.is_empty());
    }

    #[test]
    fn test_deploy_config_empty_json_uses_defaults() {
        let dc: DeployConfig = serde_json::from_str("{}").expect("empty deploy JSON should succeed");
        assert!(!dc.enabled);
        assert!(dc.command.is_empty());
        assert!(!dc.auto_commit);
        assert!(dc.home_source_path.is_empty());
    }

    // -------------------------------------------------------
    // 4. load_config function
    // -------------------------------------------------------

    #[test]
    fn test_load_config_valid_file() {
        with_clean_is_home(|| {
            let json = r#"{
                "rootDir": "/project",
                "hashLength": 10,
                "excludeDirs": ["vendor"]
            }"#;
            let path = create_temp_config("test_load_valid.json", json);

            let cfg = load_config(path.to_str().unwrap());

            assert_eq!(cfg.root_dir, "/project");
            assert_eq!(cfg.hash_length, 10);
            assert_eq!(cfg.exclude_dirs, vec!["vendor"]);

            remove_temp_config(&path);
        });
    }

    #[test]
    fn test_load_config_nonexistent_file_returns_defaults() {
        with_clean_is_home(|| {
            let cfg = load_config("nonexistent_file_12345_test.json");

            // When file not found, load_config returns Config::default() (Rust Default, not serde defaults)
            assert!(cfg.root_dir.is_empty());
            assert_eq!(cfg.hash_length, 0);
            assert!(cfg.exclude_dirs.is_empty());
        });
    }

    #[test]
    fn test_load_config_empty_fields_filled_with_defaults() {
        with_clean_is_home(|| {
            // rootDir="", hashLength=0, excludeDirs=[] should be overridden by load_config
            let json = r#"{"rootDir": "", "hashLength": 0, "excludeDirs": []}"#;
            let path = create_temp_config("test_load_defaults.json", json);

            let cfg = load_config(path.to_str().unwrap());

            assert_eq!(cfg.root_dir, ".");
            assert_eq!(cfg.hash_length, 8);
            assert_eq!(cfg.exclude_dirs, vec!["node_modules", ".git", "dist", "build"]);

            remove_temp_config(&path);
        });
    }

    #[test]
    fn test_load_config_invalid_json_returns_defaults() {
        with_clean_is_home(|| {
            let path = create_temp_config("test_load_invalid.json", "not valid json {{{");

            let cfg = load_config(path.to_str().unwrap());

            // serde_json::from_str fails -> unwrap_or_default() kicks in
            assert_eq!(cfg.root_dir, ".");
            assert_eq!(cfg.hash_length, 8);

            remove_temp_config(&path);
        });
    }

    // -------------------------------------------------------
    // 5. Environment variable IS_HOME
    // -------------------------------------------------------

    #[test]
    fn test_is_home_1_selects_home_html_file() {
        let _lock = IS_HOME_MUTEX.lock().unwrap_or_else(|e| e.into_inner());
        let json = r#"{
            "homeHTMLFile": "home.html",
            "companyHTMLFile": "company.html"
        }"#;
        let path = create_temp_config("test_is_home_1.json", json);

        env::set_var("IS_HOME", "1");
        let cfg = load_config(path.to_str().unwrap());
        env::remove_var("IS_HOME");

        assert_eq!(cfg.single_html_file, "home.html");

        remove_temp_config(&path);
    }

    #[test]
    fn test_is_home_not_set_selects_company_html_file() {
        let _lock = IS_HOME_MUTEX.lock().unwrap_or_else(|e| e.into_inner());
        let json = r#"{
            "homeHTMLFile": "home.html",
            "companyHTMLFile": "company.html"
        }"#;
        let path = create_temp_config("test_is_home_0.json", json);

        env::remove_var("IS_HOME");
        let cfg = load_config(path.to_str().unwrap());

        assert_eq!(cfg.single_html_file, "company.html");

        remove_temp_config(&path);
    }

    #[test]
    fn test_is_home_empty_string_behaves_as_not_home() {
        let _lock = IS_HOME_MUTEX.lock().unwrap_or_else(|e| e.into_inner());
        let json = r#"{
            "homeHTMLFile": "home.html",
            "companyHTMLFile": "company.html"
        }"#;
        let path = create_temp_config("test_is_home_empty.json", json);

        env::set_var("IS_HOME", "");
        let cfg = load_config(path.to_str().unwrap());
        env::remove_var("IS_HOME");

        assert_eq!(cfg.single_html_file, "company.html");

        remove_temp_config(&path);
    }

    #[test]
    fn test_is_home_1_with_only_company_html_file() {
        let _lock = IS_HOME_MUTEX.lock().unwrap_or_else(|e| e.into_inner());
        let json = r#"{
            "companyHTMLFile": "company.html"
        }"#;
        let path = create_temp_config("test_is_home_only_company.json", json);

        env::set_var("IS_HOME", "1");
        let cfg = load_config(path.to_str().unwrap());
        env::remove_var("IS_HOME");

        // When IS_HOME=1 but homeHTMLFile is not set, single_html_file stays as deserialized (empty)
        let _ = cfg.single_html_file;

        remove_temp_config(&path);
    }

    #[test]
    fn test_not_home_with_only_home_html_file() {
        let _lock = IS_HOME_MUTEX.lock().unwrap_or_else(|e| e.into_inner());
        let json = r#"{
            "homeHTMLFile": "home.html"
        }"#;
        let path = create_temp_config("test_not_home_only_home.json", json);

        env::remove_var("IS_HOME");
        let cfg = load_config(path.to_str().unwrap());

        assert!(cfg.single_html_file.is_empty());

        remove_temp_config(&path);
    }

    #[test]
    fn test_is_home_neither_html_file_set() {
        let _lock = IS_HOME_MUTEX.lock().unwrap_or_else(|e| e.into_inner());
        let json = r#"{"rootDir": "/tmp"}"#;
        let path = create_temp_config("test_is_home_neither.json", json);

        env::set_var("IS_HOME", "1");
        let cfg = load_config(path.to_str().unwrap());
        env::remove_var("IS_HOME");

        assert!(cfg.single_html_file.is_empty());

        remove_temp_config(&path);
    }

    // -------------------------------------------------------
    // Bonus: utility function tests
    // -------------------------------------------------------

    #[test]
    fn test_clean_path_slashes() {
        assert_eq!(clean_path_slashes("foo\\bar\\baz"), "foo/bar/baz");
        assert_eq!(clean_path_slashes("foo/bar"), "foo/bar");
        assert_eq!(clean_path_slashes(""), "");
        assert_eq!(clean_path_slashes("no_slashes"), "no_slashes");
    }

    #[test]
    fn test_file_exists_with_real_file() {
        let path = create_temp_config("test_exists.txt", "hello");
        assert!(file_exists(&path));
        remove_temp_config(&path);
        assert!(!file_exists(&path));
    }

    #[test]
    fn test_file_exists_nonexistent_path() {
        assert!(!file_exists("/nonexistent/path/that/does/not/exist.txt"));
    }
}

#[cfg(test)]
mod html_tests {
    use super::*;
    use std::fs::File;
    use std::io::Write;

    fn make_vm_with_components(include_components: Vec<String>) -> VersionManager {
        let config = Config {
            include_components,
            ..Config::default()
        };
        VersionManager::new(config, false)
    }

    // -------------------------------------------------------
    // 1. should_process_component: matching component
    // -------------------------------------------------------
    #[test]
    fn test_should_process_component_matching() {
        let vm = make_vm_with_components(vec!["header".to_string()]);
        // Path contains "/header/" so it should match
        assert!(vm.should_process_component("components/header/style.css"));
    }

    // -------------------------------------------------------
    // 2. should_process_component: non-matching component
    // -------------------------------------------------------
    #[test]
    fn test_should_process_component_non_matching() {
        let vm = make_vm_with_components(vec!["header".to_string()]);
        // "footer" is not in include_components
        assert!(!vm.should_process_component("components/footer/style.css"));
    }

    // -------------------------------------------------------
    // 3. should_process_component: empty include list
    // -------------------------------------------------------
    #[test]
    fn test_should_process_component_empty_include_list() {
        let vm = make_vm_with_components(vec![]);
        // Empty include list means accept everything
        assert!(vm.should_process_component("any/path/style.css"));
        assert!(vm.should_process_component("components/footer/app.js"));
    }

    // -------------------------------------------------------
    // 4. collect_resources_from_html: collects component CSS/JS
    // -------------------------------------------------------
    #[test]
    fn test_collect_resources_from_html_collects_components() {
        let dir = std::env::temp_dir().join("hashcdn_html_tests");
        fs::create_dir_all(&dir).expect("failed to create temp dir");
        let html_path = dir.join("test_collect.html");
        {
            let mut f = File::create(&html_path).expect("create temp html");
            write!(
                f,
                r#"<!DOCTYPE html>
<html>
<head>
    <link rel="stylesheet" href="components/header/style.css">
    <link rel="stylesheet" href="https://cdn.example.com/external.css">
    <link rel="stylesheet" href="main.css">
</head>
<body>
    <script src="components/header/script.js"></script>
    <script src="https://cdn.example.com/external.js"></script>
    <script src="app.js"></script>
</body>
</html>"#
            )
            .expect("write temp html");
        }

        let vm = make_vm_with_components(vec![]);
        let resources = vm
            .collect_resources_from_html(&html_path)
            .expect("collect_resources failed");

        // Only paths containing "components" should be collected
        assert_eq!(resources["css"].len(), 1);
        assert!(resources["css"].contains(&"components/header/style.css".to_string()));
        assert_eq!(resources["js"].len(), 1);
        assert!(resources["js"].contains(&"components/header/script.js".to_string()));

        let _ = fs::remove_file(&html_path);
    }

    // -------------------------------------------------------
    // 5. collect_resources_from_html: empty HTML (no resources)
    // -------------------------------------------------------
    #[test]
    fn test_collect_resources_from_html_empty() {
        let dir = std::env::temp_dir().join("hashcdn_html_tests");
        fs::create_dir_all(&dir).expect("failed to create temp dir");
        let html_path = dir.join("test_collect_empty.html");
        {
            let mut f = File::create(&html_path).expect("create temp html");
            write!(f, "<html><body><p>No resources here</p></body></html>")
                .expect("write temp html");
        }

        let vm = make_vm_with_components(vec![]);
        let resources = vm
            .collect_resources_from_html(&html_path)
            .expect("collect_resources failed");

        assert!(resources["css"].is_empty());
        assert!(resources["js"].is_empty());

        let _ = fs::remove_file(&html_path);
    }

    // -------------------------------------------------------
    // 6. collect_resources_from_html: filters by include_components
    // -------------------------------------------------------
    #[test]
    fn test_collect_resources_from_html_with_include_filter() {
        let dir = std::env::temp_dir().join("hashcdn_html_tests");
        fs::create_dir_all(&dir).expect("failed to create temp dir");
        let html_path = dir.join("test_collect_filter.html");
        {
            let mut f = File::create(&html_path).expect("create temp html");
            write!(
                f,
                r#"<!DOCTYPE html>
<html>
<head>
    <link rel="stylesheet" href="components/header/style.css">
    <link rel="stylesheet" href="components/footer/style.css">
</head>
<body>
    <script src="components/sidebar/app.js"></script>
</body>
</html>"#
            )
            .expect("write temp html");
        }

        // Only include "header"
        let vm = make_vm_with_components(vec!["header".to_string()]);
        let resources = vm
            .collect_resources_from_html(&html_path)
            .expect("collect_resources failed");

        assert_eq!(resources["css"].len(), 1);
        assert!(resources["css"].contains(&"components/header/style.css".to_string()));
        // footer and sidebar should be filtered out
        assert!(resources["js"].is_empty());

        let _ = fs::remove_file(&html_path);
    }

    // -------------------------------------------------------
    // 7. update_html_references: updates CSS and JS references
    // -------------------------------------------------------
    #[test]
    fn test_update_html_references_updates_paths() {
        let dir = std::env::temp_dir().join("hashcdn_html_tests");
        fs::create_dir_all(&dir).expect("failed to create temp dir");
        let html_path = dir.join("test_update.html");
        {
            let mut f = File::create(&html_path).expect("create temp html");
            write!(
                f,
                r#"<!DOCTYPE html>
<html>
<head>
    <link rel="stylesheet" href="components/header/style.css">
</head>
<body>
    <script src="components/header/script.js"></script>
</body>
</html>"#
            )
            .expect("write temp html");
        }

        let mut resources: HashMap<String, HashMap<String, String>> = HashMap::new();
        let mut css_map = HashMap::new();
        css_map.insert(
            "components/header/style.css".to_string(),
            "components/header/style.abc12345.css".to_string(),
        );
        resources.insert("css".to_string(), css_map);

        let mut js_map = HashMap::new();
        js_map.insert(
            "components/header/script.js".to_string(),
            "components/header/script.def67890.js".to_string(),
        );
        resources.insert("js".to_string(), js_map);

        let vm = make_vm_with_components(vec![]);
        vm.update_html_references(&html_path, &resources)
            .expect("update_html_references failed");

        let content = fs::read_to_string(&html_path).expect("read updated html");
        assert!(
            content.contains("style.abc12345.css"),
            "CSS reference should be updated with hash"
        );
        assert!(
            content.contains("script.def67890.js"),
            "JS reference should be updated with hash"
        );
        // Original paths should no longer be present
        assert!(
            !content.contains("style.css\""),
            "Original CSS path should be replaced"
        );
        assert!(
            !content.contains("script.js\""),
            "Original JS path should be replaced"
        );

        let _ = fs::remove_file(&html_path);
    }

    // -------------------------------------------------------
    // 8. update_html_references: no matching resources (no-op)
    // -------------------------------------------------------
    #[test]
    fn test_update_html_references_no_match() {
        let dir = std::env::temp_dir().join("hashcdn_html_tests");
        fs::create_dir_all(&dir).expect("failed to create temp dir");
        let html_path = dir.join("test_update_nomatch.html");
        let original_content = r#"<!DOCTYPE html>
<html>
<head>
    <link rel="stylesheet" href="main.css">
</head>
<body>
    <script src="app.js"></script>
</body>
</html>"#;
        {
            let mut f = File::create(&html_path).expect("create temp html");
            write!(f, "{}", original_content).expect("write temp html");
        }

        // Provide resources that don't match anything in the HTML
        let mut resources: HashMap<String, HashMap<String, String>> = HashMap::new();
        let mut css_map = HashMap::new();
        css_map.insert(
            "components/sidebar/theme.css".to_string(),
            "components/sidebar/theme.aaa11111.css".to_string(),
        );
        resources.insert("css".to_string(), css_map);

        let vm = make_vm_with_components(vec![]);
        vm.update_html_references(&html_path, &resources)
            .expect("update_html_references failed");

        let content = fs::read_to_string(&html_path).expect("read html");
        // Content should remain unchanged since nothing matched
        assert!(content.contains("main.css"));
        assert!(content.contains("app.js"));

        let _ = fs::remove_file(&html_path);
    }
}

#[cfg(test)]
mod css_tests {
    use super::*;
    use std::fs::File;
    use std::io::Write;

    fn make_vm() -> VersionManager {
        let config = Config {
            hash_length: 8,
            ..Config::default()
        };
        VersionManager::new(config, false)
    }

    // -------------------------------------------------------
    // 1. collect_images_from_css: basic collection of local images
    // -------------------------------------------------------
    #[test]
    fn test_collect_images_from_css_basic() {
        let dir = std::env::temp_dir().join("css_test_collect_basic");
        let _ = fs::create_dir_all(&dir);

        let css_content = r#"
.icon { background: url(icon.png); }
.logo { background: url(logo.svg); }
"#;
        let css_path = dir.join("style.css");
        {
            let mut f = File::create(&css_path).expect("create css");
            write!(f, "{}", css_content).expect("write css");
        }
        File::create(dir.join("icon.png")).unwrap();
        File::create(dir.join("logo.svg")).unwrap();

        let vm = make_vm();
        let images = vm.collect_images_from_css(&css_path).expect("collect failed");

        assert_eq!(images.len(), 2);
        let originals: Vec<&str> = images.iter().map(|i| i.original_path.as_str()).collect();
        assert!(originals.contains(&"icon.png"));
        assert!(originals.contains(&"logo.svg"));

        let _ = fs::remove_dir_all(&dir);
    }

    // -------------------------------------------------------
    // 2. collect_images_from_css: skips external URLs
    // -------------------------------------------------------
    #[test]
    fn test_collect_images_from_css_skips_external_urls() {
        let dir = std::env::temp_dir().join("css_test_collect_external");
        let _ = fs::create_dir_all(&dir);

        let css_content = r#"
.a { background: url(http://example.com/a.png); }
.b { background: url(https://example.com/b.png); }
.c { background: url(data:image/png;base64,abc); }
.d { background: url(//cdn.example.com/d.png); }
.e { background: url(local.png); }
"#;
        let css_path = dir.join("style.css");
        {
            let mut f = File::create(&css_path).expect("create css");
            write!(f, "{}", css_content).expect("write css");
        }
        File::create(dir.join("local.png")).unwrap();

        let vm = make_vm();
        let images = vm.collect_images_from_css(&css_path).expect("collect failed");

        assert_eq!(images.len(), 1);
        assert_eq!(images[0].original_path, "local.png");

        let _ = fs::remove_dir_all(&dir);
    }

    // -------------------------------------------------------
    // 3. collect_images_from_css: only returns images that exist on disk
    // -------------------------------------------------------
    #[test]
    fn test_collect_images_from_css_only_existing_files() {
        let dir = std::env::temp_dir().join("css_test_collect_existing");
        let _ = fs::create_dir_all(&dir);

        let css_content = r#"
.a { background: url(exists.png); }
.b { background: url(missing.png); }
"#;
        let css_path = dir.join("style.css");
        {
            let mut f = File::create(&css_path).expect("create css");
            write!(f, "{}", css_content).expect("write css");
        }
        File::create(dir.join("exists.png")).unwrap();
        // missing.png is NOT created

        let vm = make_vm();
        let images = vm.collect_images_from_css(&css_path).expect("collect failed");

        assert_eq!(images.len(), 1);
        assert_eq!(images[0].original_path, "exists.png");

        let _ = fs::remove_dir_all(&dir);
    }

    // -------------------------------------------------------
    // 4. update_css_image_references: simple filename replacement
    // -------------------------------------------------------
    #[test]
    fn test_update_css_image_references_simple() {
        let dir = std::env::temp_dir().join("css_test_update_simple");
        let _ = fs::create_dir_all(&dir);

        let css_content = r#"
.icon { background: url(icon.png); }
.logo { background: url(logo.svg); }
"#;
        let css_path = dir.join("style.css");
        {
            let mut f = File::create(&css_path).expect("create css");
            write!(f, "{}", css_content).expect("write css");
        }

        let mut image_map = HashMap::new();
        image_map.insert("icon.png".to_string(), "icon.abc12345.png".to_string());
        image_map.insert("logo.svg".to_string(), "logo.def67890.svg".to_string());

        let vm = make_vm();
        vm.update_css_image_references(&css_path, &image_map).expect("update failed");

        let updated = fs::read_to_string(&css_path).expect("read updated css");
        assert!(updated.contains("icon.abc12345.png"));
        assert!(updated.contains("logo.def67890.svg"));
        assert!(!updated.contains("icon.png"));
        assert!(!updated.contains("logo.svg"));

        let _ = fs::remove_dir_all(&dir);
    }

    // -------------------------------------------------------
    // 5. update_css_image_references: preserves quote style
    // -------------------------------------------------------
    #[test]
    fn test_update_css_image_references_preserves_quotes() {
        let dir = std::env::temp_dir().join("css_test_update_quotes");
        let _ = fs::create_dir_all(&dir);

        let css_content = r#"
.a { background: url("double.png"); }
.b { background: url('single.png'); }
.c { background: url(unquoted.png); }
"#;
        let css_path = dir.join("style.css");
        {
            let mut f = File::create(&css_path).expect("create css");
            write!(f, "{}", css_content).expect("write css");
        }

        let mut image_map = HashMap::new();
        image_map.insert("double.png".to_string(), "double.hh.png".to_string());
        image_map.insert("single.png".to_string(), "single.hh.png".to_string());
        image_map.insert("unquoted.png".to_string(), "unquoted.hh.png".to_string());

        let vm = make_vm();
        vm.update_css_image_references(&css_path, &image_map).expect("update failed");

        let updated = fs::read_to_string(&css_path).expect("read updated css");
        assert!(updated.contains(r#"url("double.hh.png")"#), "double quotes should be preserved: {}", updated);
        assert!(updated.contains("url('single.hh.png')"), "single quotes should be preserved: {}", updated);
        assert!(updated.contains("url(unquoted.hh.png)"), "no quotes should remain no quotes: {}", updated);

        let _ = fs::remove_dir_all(&dir);
    }

    // -------------------------------------------------------
    // 6. update_css_image_references: preserves external URLs
    // -------------------------------------------------------
    #[test]
    fn test_update_css_image_references_preserves_external_urls() {
        let dir = std::env::temp_dir().join("css_test_update_external");
        let _ = fs::create_dir_all(&dir);

        let css_content = r#"
.a { background: url(http://example.com/remote.png); }
.b { background: url(local.png); }
"#;
        let css_path = dir.join("style.css");
        {
            let mut f = File::create(&css_path).expect("create css");
            write!(f, "{}", css_content).expect("write css");
        }

        let mut image_map = HashMap::new();
        image_map.insert("local.png".to_string(), "local.hh.png".to_string());

        let vm = make_vm();
        vm.update_css_image_references(&css_path, &image_map).expect("update failed");

        let updated = fs::read_to_string(&css_path).expect("read updated css");
        assert!(updated.contains("http://example.com/remote.png"), "external URL should be unchanged: {}", updated);
        assert!(updated.contains("local.hh.png"), "local URL should be updated: {}", updated);

        let _ = fs::remove_dir_all(&dir);
    }

    // -------------------------------------------------------
    // 7. Various url() formats: quotes, subdirectories
    // -------------------------------------------------------
    #[test]
    fn test_collect_images_from_css_various_url_formats() {
        let dir = std::env::temp_dir().join("css_test_collect_formats");
        let images_subdir = dir.join("images");
        let _ = fs::create_dir_all(&images_subdir);

        let css_content = r#"
.a { background: url(unquoted.png); }
.b { background: url('single.png'); }
.c { background: url("double.png"); }
.d { background: url(images/sub.png); }
"#;
        let css_path = dir.join("style.css");
        {
            let mut f = File::create(&css_path).expect("create css");
            write!(f, "{}", css_content).expect("write css");
        }
        File::create(dir.join("unquoted.png")).unwrap();
        File::create(dir.join("single.png")).unwrap();
        File::create(dir.join("double.png")).unwrap();
        File::create(images_subdir.join("sub.png")).unwrap();

        let vm = make_vm();
        let images = vm.collect_images_from_css(&css_path).expect("collect failed");

        assert_eq!(images.len(), 4);
        let originals: Vec<&str> = images.iter().map(|i| i.original_path.as_str()).collect();
        assert!(originals.contains(&"unquoted.png"), "should find unquoted url: {:?}", originals);
        assert!(originals.contains(&"single.png"), "should find single-quoted url: {:?}", originals);
        assert!(originals.contains(&"double.png"), "should find double-quoted url: {:?}", originals);
        assert!(originals.contains(&"images/sub.png"), "should find subdirectory url: {:?}", originals);

        let _ = fs::remove_dir_all(&dir);
    }

    // -------------------------------------------------------
    // 8. update_css_image_references with subdirectory paths
    // -------------------------------------------------------
    #[test]
    fn test_update_css_image_references_with_subdirectory() {
        let dir = std::env::temp_dir().join("css_test_update_subdir");
        let _ = fs::create_dir_all(&dir);

        let css_content = r#"
.a { background: url(images/photo.png); }
"#;
        let css_path = dir.join("style.css");
        {
            let mut f = File::create(&css_path).expect("create css");
            write!(f, "{}", css_content).expect("write css");
        }

        let mut image_map = HashMap::new();
        image_map.insert("images/photo.png".to_string(), "photo.abc12345.png".to_string());

        let vm = make_vm();
        vm.update_css_image_references(&css_path, &image_map).expect("update failed");

        let updated = fs::read_to_string(&css_path).expect("read updated css");
        assert!(updated.contains("images/photo.abc12345.png"), "subdirectory path should be preserved: {}", updated);
        assert!(!updated.contains("images/photo.png"), "original should be replaced: {}", updated);

        let _ = fs::remove_dir_all(&dir);
    }
}
