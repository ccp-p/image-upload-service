//! hash-cdn: HTML hash CDN tool (Rust edition).
//! Zero external dependencies - pure std lib for minimal binary on low-spec devices.

mod config;
mod deploy;
mod json;
mod md5;
mod patterns;
mod version_manager;

use config::Config;
use deploy::DeployManager;
use version_manager::VersionManager;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let mut config_path = "version.config.json".to_string();
    let mut html_file = String::new();
    let mut scan_all = false;
    let mut cdn_domain = String::new();
    let mut debug_mode = false;
    let mut deploy_only = false;
    let mut deploy_commit = false;
    let mut deploy_mode: i32 = 6;
    let mut commit_message = String::new();
    let mut dry_run = false;
    let mut revert_svn = false;
    let mut revert_git = false;

    let start = std::time::Instant::now();

    let mut i = 1;
    while i < args.len() {
        // Support both "-flag value" and "-flag=value" syntax (like Go's flag package)
        let (key, inline_val): (&str, Option<&str>) = match args[i].split_once('=') {
            Some((k, v)) if k.starts_with('-') => (k, Some(v)),
            _ => (args[i].as_str(), None),
        };

        let take_val = |i: &mut usize, inline: Option<&str>| -> Option<String> {
            if let Some(v) = inline {
                Some(v.to_string())
            } else {
                *i += 1;
                if *i < args.len() {
                    Some(args[*i].clone())
                } else {
                    None
                }
            }
        };

        match key {
            "-config" => {
                if let Some(v) = take_val(&mut i, inline_val) {
                    config_path = v;
                }
            }
            "-file" => {
                if let Some(v) = take_val(&mut i, inline_val) {
                    html_file = v;
                }
            }
            "-all" => scan_all = true,
            "-cdn" => {
                if let Some(v) = take_val(&mut i, inline_val) {
                    cdn_domain = v;
                }
            }
            "-debug" => debug_mode = true,
            "-deploy" => deploy_only = true,
            "-deploy-commit" => deploy_commit = true,
            "-mode" => {
                if let Some(v) = take_val(&mut i, inline_val) {
                    deploy_mode = v.parse().unwrap_or(6);
                }
            }
            "-message" => {
                if let Some(v) = take_val(&mut i, inline_val) {
                    commit_message = v;
                }
            }
            "-dry-run" => dry_run = true,
            "-revert-svn" => revert_svn = true,
            "-revert-git" => revert_git = true,
            "-h" | "--help" | "-help" => {
                print_usage();
                return;
            }
            _ => {}
        }
        i += 1;
    }

    println!("📂 加载配置文件: {}", config_path);
    let mut config = match config::load_config(&config_path) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("⚠️ 配置错误(使用默认值): {}", e);
            Config::default()
        }
    };

    // Print environment info (mirrors Go output)
    let is_home = config::is_home_env();
    println!("🏠 运行环境: {}", if is_home { "Home" } else { "Office" });
    if !config.single_html_file.is_empty() {
        let label = if is_home { "Home" } else { "Office" };
        println!("📄 HTML文件 ({}): {}", label, config.single_html_file);
    }

    if !cdn_domain.is_empty() {
        config.cdn_domain = cdn_domain.clone();
    }

    apply_deploy_mode(&mut config, deploy_mode);
    println!("📋 部署模式: {}", deploy_mode);

    // Revert local changes: dest SVN (-revert-svn) and/or src git (-revert-git).
    if revert_svn || revert_git {
        let dm = DeployManager::new(config.deploy.clone(), debug_mode);
        if revert_svn {
            if let Err(e) = dm.revert_all_svn() {
                eprintln!("❌ SVN回退失败: {}", e);
                std::process::exit(1);
            }
        }
        if revert_git {
            if let Err(e) = dm.revert_src_git() {
                eprintln!("❌ Git回退失败: {}", e);
                std::process::exit(1);
            }
        }
        print_total_elapsed(start);
        return;
    }

    if dry_run {
        println!("\n=== dry-run preview ===");
        println!("  html file:    {}", config.single_html_file);
        println!("  cdn domain:   {}", config.cdn_domain);
        println!("  hash length:  {}", config.hash_length);
        println!(
            "  deploy:       enabled={}, command={}, autoCommit={}",
            config.deploy.enabled, config.deploy.command, config.deploy.auto_commit
        );
        println!(
            "  rollback:     {}, git commit: {}",
            config.rollback_after_deploy, config.git_commit_after_rollback
        );
        println!("  src (Home):   {}", config.deploy.home_source_path);
        println!("  dst (Home):   {}", config.deploy.home_dest_path);
        println!("  src (Office): {}", config.deploy.company_source_path);
        println!("  dst (Office): {}", config.deploy.company_dest_path);
        println!("  deploy files ({}):", config.deploy.file_paths.len());
        for fp in &config.deploy.file_paths {
            println!("    - {}", fp);
        }
        println!("  (dry-run, no files modified)");
        return;
    }

    if deploy_only || deploy_commit {
        if deploy_commit {
            config.deploy.auto_commit = true;
            config.deploy.command = "copy-commit".to_string();
        }
        run_deploy(&config, debug_mode, &commit_message, dry_run);
        print_total_elapsed(start);
        return;
    }

    // Process HTML files (hash resources, update references)
    let mut vm = VersionManager::new(config.clone(), debug_mode);
    vm.commit_message = commit_message.clone();

    if dry_run {
        println!("[dry-run] no files will be modified");
    }

    if scan_all {
        let files = vm.find_all_html_files();
        println!("found {} HTML files", files.len());
        if !dry_run {
            vm.process_multiple_html_files(&files);
        }
    } else if !html_file.is_empty() {
        config.single_html_file = html_file.clone();
        if !dry_run {
            if let Err(e) = vm.process_html_file(&html_file) {
                eprintln!("error processing {}: {}", html_file, e);
            }
        }
    } else if !config.single_html_file.is_empty() {
        let abs = join_path(&config.root_dir, &config.single_html_file);
        if !dry_run {
            if let Err(e) = vm.process_html_file(&abs) {
                eprintln!("error processing {}: {}", abs, e);
            }
        }
    } else if !config.html_files.is_empty() {
        if !dry_run {
            vm.process_multiple_html_files(&config.html_files);
        }
    } else {
        let files = vm.find_all_html_files();
        println!("found {} HTML files", files.len());
        if !dry_run {
            vm.process_multiple_html_files(&files);
        }
    }

    // Run deploy if enabled
    if config.deploy.enabled {
        run_deploy(&config, debug_mode, &commit_message, dry_run);
    }
    println!("\n✨ 处理完成!");
    print_total_elapsed(start);
}

/// Applies deploy mode settings to the config, mirroring Go's mode constants.
fn apply_deploy_mode(config: &mut Config, mode: i32) {
    if mode <= 0 {
        return;
    }
    config.deploy.enabled = true;
    let auto_commit = matches!(mode, 2 | 3 | 5 | 6 | 8 | 9);
    let no_cdn = matches!(mode, 4 | 5 | 6);
    let clear_excludes = matches!(mode, 7 | 8 | 9);
    let rollback = matches!(mode, 3 | 6 | 9);

    config.deploy.auto_commit = auto_commit;
    config.deploy.command = if auto_commit { "copy-commit" } else { "copy" }.to_string();
    if no_cdn {
        config.cdn_domain = String::new();
    }
    if clear_excludes {
        config.cdn_exclude_files = Vec::new();
    }
    config.rollback_after_deploy = rollback;
    config.git_commit_after_rollback = rollback;
}

/// Creates a DeployManager and runs the deploy workflow, then always rolls back
/// the source HTML file so it is not left with CDN references.
fn run_deploy(config: &Config, debug_mode: bool, commit_message: &str, dry_run: bool) {
    if dry_run {
        println!(
            "[dry-run] 部署预览: 将复制 {} 个文件路径",
            config.deploy.file_paths.len()
        );
        return;
    }

    println!();
    println!("{}", "=".repeat(60));
    println!("🚀 开始部署流程");
    println!("{}", "=".repeat(60));

    let mut dm = DeployManager::new(config.deploy.clone(), debug_mode);
    if let Err(e) = dm.run(config.deploy.auto_commit, commit_message, &config.single_html_file, &config.cdn_domain) {
        eprintln!("❌ 部署失败: {}", e);
        return;
    }

    // Always roll back the source HTML after copy so it is not left modified.
    // Git commit/push only runs for the rollback modes (3/6/9).
    if !config.single_html_file.is_empty() {
        let _ = deploy::rollback_html_file(&config.single_html_file);
        if config.git_commit_after_rollback {
            let _ = deploy::git_commit_and_push_after_rollback(&config.single_html_file);
        }
    }
}

fn join_path(dir: &str, name: &str) -> String {
    let name = name.trim_start_matches(|c| c == '/' || c == '\\');
    std::path::PathBuf::from(dir)
        .join(name)
        .to_string_lossy()
        .to_string()
}

/// Prints total runtime since `start`, mirroring Go's
/// `fmt.Printf` duration print at each exit point.
fn print_total_elapsed(start: std::time::Instant) {
    println!("\n⏱️  总运行时间: {:?}", start.elapsed());
}

fn print_usage() {
    eprintln!("hash-cdn (Rust) - HTML hash CDN tool");
    eprintln!();
    eprintln!("Usage: hash-cdn [flags]");
    eprintln!();
    eprintln!("Flags:");
    eprintln!("  -config <path>      config file (default: version.config.json)");
    eprintln!("  -file <path>        process a single HTML file");
    eprintln!("  -all                scan and process all HTML files");
    eprintln!("  -cdn <domain>       CDN domain override");
    eprintln!("  -debug              verbose debug output");
    eprintln!("  -deploy             deploy only (skip hash processing)");
    eprintln!("  -deploy-commit      deploy with auto-commit");
    eprintln!("  -mode <n>           deploy mode 1-9 (default: 6)");
    eprintln!("  -message <text>     custom commit message");
    eprintln!("  -dry-run            preview without modifying files");
    eprintln!("  -revert-svn         revert dest SVN local changes + remove unversioned files");
    eprintln!("  -revert-git         revert src git working tree (git reset --hard + git clean -fd)");
}
