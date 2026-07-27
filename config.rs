//! Configuration structures and loading logic.

use crate::json::JsonValue;

#[derive(Debug, Clone)]
pub struct Config {
    pub root_dir: String,
    pub cdn_domain: String,
    pub hash_length: usize,
    pub single_html_file: String,
    pub html_files: Vec<String>,
    pub exclude_dirs: Vec<String>,
    pub home_html_file: String,
    pub company_html_file: String,
    pub include_components: Vec<String>,
    pub process_main_resources: Vec<String>,
    pub replace_all_with_cdn: bool,
    pub rollback_after_deploy: bool,
    pub git_commit_after_rollback: bool,
    pub cdn_exclude_files: Vec<String>,
    pub deploy: DeployConfig,
}

#[derive(Debug, Clone)]
pub struct DeployConfig {
    pub enabled: bool,
    pub command: String,
    pub auto_commit: bool,
    pub home_source_path: String,
    pub home_dest_path: String,
    pub company_source_path: String,
    pub company_dest_path: String,
    pub file_paths: Vec<String>,
    pub git_authors: Vec<String>,
    pub cdn_path_prefix: String,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            root_dir: ".".to_string(),
            cdn_domain: String::new(),
            hash_length: 8,
            single_html_file: String::new(),
            html_files: Vec::new(),
            exclude_dirs: vec![
                "node_modules".into(),
                ".git".into(),
                "dist".into(),
                "build".into(),
            ],
            home_html_file: String::new(),
            company_html_file: String::new(),
            include_components: Vec::new(),
            process_main_resources: Vec::new(),
            replace_all_with_cdn: false,
            rollback_after_deploy: false,
            git_commit_after_rollback: false,
            cdn_exclude_files: Vec::new(),
            deploy: DeployConfig::default(),
        }
    }
}

impl Default for DeployConfig {
    fn default() -> Self {
        DeployConfig {
            enabled: false,
            command: String::new(),
            auto_commit: false,
            home_source_path: String::new(),
            home_dest_path: String::new(),
            company_source_path: String::new(),
            company_dest_path: String::new(),
            file_paths: Vec::new(),
            git_authors: Vec::new(),
            cdn_path_prefix: String::new(),
        }
    }
}

pub fn is_home_env() -> bool {
    std::env::var("IS_HOME").unwrap_or_default() == "1"
}

pub fn load_config(config_path: &str) -> Result<Config, String> {
    let content = std::fs::read_to_string(config_path)
        .map_err(|e| format!("Failed to read config file {}: {}", config_path, e))?;

    let json = JsonValue::parse(&content)?;

    let mut config = Config::default();

    if let Some(s) = json.get_str("rootDir") {
        config.root_dir = s.to_string();
    }
    if let Some(s) = json.get_str("cdnDomain") {
        config.cdn_domain = s.to_string();
    }
    if let Some(n) = json.get_num("hashLength") {
        config.hash_length = n as usize;
    }
    if let Some(s) = json.get_str("singleHTMLFile") {
        config.single_html_file = s.to_string();
    }
    if let Some(arr) = json.get_array_str("htmlFiles") {
        config.html_files = arr;
    }
    if let Some(arr) = json.get_array_str("excludeDirs") {
        if !arr.is_empty() {
            config.exclude_dirs = arr;
        }
    }
    if let Some(s) = json.get_str("homeHTMLFile") {
        config.home_html_file = s.to_string();
    }
    if let Some(s) = json.get_str("companyHTMLFile") {
        config.company_html_file = s.to_string();
    }
    if let Some(arr) = json.get_array_str("includeComponents") {
        config.include_components = arr;
    }
    if let Some(arr) = json.get_array_str("processMainResources") {
        config.process_main_resources = arr;
    }
    if let Some(b) = json.get_bool("replaceAllWithCDN") {
        config.replace_all_with_cdn = b;
    }
    if let Some(b) = json.get_bool("rollbackAfterDeploy") {
        config.rollback_after_deploy = b;
    }
    if let Some(b) = json.get_bool("gitCommitAfterRollback") {
        config.git_commit_after_rollback = b;
    }
    if let Some(arr) = json.get_array_str("cdnExcludeFiles") {
        config.cdn_exclude_files = arr;
    }

    // Parse deploy config
    if let Some(deploy_json) = json.get_object("deploy") {
        let mut deploy = DeployConfig::default();
        if let Some(b) = deploy_json.get_bool("enabled") {
            deploy.enabled = b;
        }
        if let Some(s) = deploy_json.get_str("command") {
            deploy.command = s.to_string();
        }
        if let Some(b) = deploy_json.get_bool("autoCommit") {
            deploy.auto_commit = b;
        }
        if let Some(s) = deploy_json.get_str("homeSourcePath") {
            deploy.home_source_path = s.to_string();
        }
        if let Some(s) = deploy_json.get_str("homeDestPath") {
            deploy.home_dest_path = s.to_string();
        }
        if let Some(s) = deploy_json.get_str("companySourcePath") {
            deploy.company_source_path = s.to_string();
        }
        if let Some(s) = deploy_json.get_str("companyDestPath") {
            deploy.company_dest_path = s.to_string();
        }
        if let Some(arr) = deploy_json.get_array_str("filePaths") {
            deploy.file_paths = arr;
        }
        if let Some(arr) = deploy_json.get_array_str("gitAuthors") {
            deploy.git_authors = arr;
        }
        if let Some(s) = deploy_json.get_str("cdnPathPrefix") {
            deploy.cdn_path_prefix = s.to_string();
        }
        config.deploy = deploy;
    }

    // Environment-based path selection
    let is_home = is_home_env();
    if is_home {
        if !config.home_html_file.is_empty() {
            config.single_html_file = config.home_html_file.clone();
        }
    } else {
        if !config.company_html_file.is_empty() {
            config.single_html_file = config.company_html_file.clone();
        }
    }

    Ok(config)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_load_config() {
        let config_data = r#"{
            "rootDir": "./src",
            "cdnDomain": "https://cdn.example.com",
            "hashLength": 10,
            "htmlFiles": ["index.html"],
            "excludeDirs": ["node_modules"]
        }"#;

        let tmp = std::env::temp_dir().join(format!("hashcdn_test_{}.json", std::process::id()));
        let mut f = std::fs::File::create(&tmp).unwrap();
        f.write_all(config_data.as_bytes()).unwrap();

        let config = load_config(tmp.to_str().unwrap()).unwrap();
        let _ = std::fs::remove_file(&tmp);

        assert_eq!(config.root_dir, "./src");
        assert_eq!(config.cdn_domain, "https://cdn.example.com");
        assert_eq!(config.hash_length, 10);
        assert_eq!(config.html_files, vec!["index.html".to_string()]);
        assert_eq!(config.exclude_dirs, vec!["node_modules".to_string()]);
    }

    #[test]
    fn test_default_config() {
        let config = Config::default();
        assert_eq!(config.root_dir, ".");
        assert_eq!(config.hash_length, 8);
        assert_eq!(config.exclude_dirs.len(), 4);
    }

    #[test]
    fn test_is_home_env() {
        let original = std::env::var("IS_HOME").ok();
        std::env::set_var("IS_HOME", "1");
        assert!(is_home_env());
        std::env::set_var("IS_HOME", "0");
        assert!(!is_home_env());
        std::env::set_var("IS_HOME", "");
        assert!(!is_home_env());
        // Restore
        if let Some(v) = original {
            std::env::set_var("IS_HOME", v);
        } else {
            std::env::remove_var("IS_HOME");
        }
    }
}
