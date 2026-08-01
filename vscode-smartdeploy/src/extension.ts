import * as vscode from "vscode";
import * as http from "http";
import * as path from "path";
import * as fs from "fs";
import { execFile } from "child_process";

// Debug log: append upload request/response details to a temp file so that
// failures which never surface a notification can still be diagnosed.
// Enabled via the smartdeploy.debugLog setting.
function debugLog(msg: string) {
    const enabled = vscode.workspace.getConfiguration("smartdeploy").get<boolean>("debugLog", false);
    if (!enabled) return;
    const logPath = path.join(require("os").tmpdir(), "smartdeploy-debug.log");
    const line = `${new Date().toISOString()} ${msg}\n`;
    try { fs.appendFileSync(logPath, line); } catch { /* ignore */ }
}

function getApiUrl(): string {
    const port = vscode.workspace.getConfiguration("smartdeploy").get<number>("apiPort", 9721);
    return `http://127.0.0.1:${port}`;
}

interface UploadResponse {
    uploaded: number;
    failed: number;
    results: Array<{
        localPath: string;
        remotePath?: string;
        success: boolean;
        error?: string;
    }>;
}

interface StatusInfo {
    connected: boolean;
    pending: number;
    lastUpload: string;
}

function runRemoteCommand(endpoint: string): Promise<string> {
    return new Promise((resolve, reject) => {
        const url = getApiUrl() + endpoint;
        const req = http.request(url, { method: "POST", timeout: 180000 }, (res) => {
            let data = "";
            res.on("data", (chunk) => data += chunk);
            res.on("end", () => {
                try {
                    const parsed = JSON.parse(data);
                    if (res.statusCode !== undefined && res.statusCode >= 200 && res.statusCode < 300) {
                        resolve(parsed.output || "");
                    } else {
                        reject(new Error(parsed.error || `HTTP ${res.statusCode}`));
                    }
                } catch {
                    reject(new Error(`Invalid response: ${data}`));
                }
            });
        });
        req.on("error", (err) => reject(new Error(`Cannot connect to SmartDeploy. Is it running? (${err.message})`)));
        req.on("timeout", () => { req.destroy(); reject(new Error("Request timed out")); });
        req.end();
    });
}

function clearTemp(): Promise<string> {
    return runRemoteCommand("/clear");
}

function syncToWebapp(): Promise<string> {
    return runRemoteCommand("/sync");
}

function uploadFiles(filePaths: string[]): Promise<UploadResponse> {
    return new Promise((resolve, reject) => {
        const url = getApiUrl() + "/upload";
        const body = JSON.stringify({ paths: filePaths });
        debugLog(`POST /upload ${filePaths.length} files: ${JSON.stringify(filePaths)}`);

        const req = http.request(url, {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "Content-Length": Buffer.byteLength(body),
            },
            timeout: 120000,
        }, (res) => {
            let data = "";
            res.on("data", (chunk) => data += chunk);
            res.on("end", () => {
                debugLog(`/upload response: status=${res.statusCode} body=${data}`);
                if (res.statusCode === undefined) {
                    reject(new Error("No status code"));
                    return;
                }
                try {
                    const parsed = JSON.parse(data);
                    if (res.statusCode >= 200 && res.statusCode < 300) {
                        resolve(parsed as UploadResponse);
                    } else {
                        const p = parsed as any;
                        let errMsg = p.error || `HTTP ${res.statusCode}`;
                        // The /upload endpoint returns per-file results on
                        // failure (e.g. 500 when every file fails). Surface
                        // the first failure's reason so the user sees *why*,
                        // not just the bare status code.
                        if (Array.isArray(p.results)) {
                            const failed = (p.results as any[]).filter((r) => !r.success);
                            if (failed.length > 0) {
                                const detail = failed[0].error || "unknown";
                                errMsg = `${errMsg} (${failed.length} failed: ${detail})`;
                            }
                        }
                        reject(new Error(errMsg));
                    }
                } catch {
                    reject(new Error(`Invalid response: ${data}`));
                }
            });
        });

        req.on("error", (err) => {
            reject(new Error(`Cannot connect to SmartDeploy. Is it running? (${err.message})`));
        });

        req.on("timeout", () => {
            req.destroy();
            reject(new Error("Request timed out"));
        });

        req.write(body);
        req.end();
    });
}

function uploadSingleFile(filePath: string): Promise<UploadResponse> {
    return uploadFiles([filePath]);
}

function checkStatus(): Promise<StatusInfo> {
    return new Promise((resolve, reject) => {
        http.get(getApiUrl() + "/status", (res) => {
            let data = "";
            res.on("data", (chunk) => data += chunk);
            res.on("end", () => {
                try {
                    resolve(JSON.parse(data));
                } catch {
                    reject(new Error("Invalid response"));
                }
            });
        }).on("error", (err) => {
            reject(new Error(`Cannot connect to SmartDeploy. Is it running? (${err.message})`));
        });
    });
}

// expandDirectory walks a directory recursively and returns all file paths.
// This lets the user right-click a folder and upload everything inside.
function expandDirectory(dirPath: string): string[] {
    const results: string[] = [];
    function walk(dir: string) {
        let entries: string[];
        try {
            entries = fs.readdirSync(dir);
        } catch {
            return;
        }
        for (const entry of entries) {
            const fullPath = path.join(dir, entry);
            try {
                const stat = fs.statSync(fullPath);
                if (stat.isDirectory()) {
                    walk(fullPath);
                } else if (stat.isFile()) {
                    results.push(fullPath);
                }
            } catch {
                // skip unreadable entries
            }
        }
    }
    walk(dirPath);
    return results;
}

// resolveUris accepts the arguments VSCode passes to an explorer/editor/tab/SCM
// context menu command. These can be: a single Uri, an array of Uris, a single
// scm.ResourceState (which exposes `resourceUri`), an array of them, or a Tab
// object (whose `input.uri` is the resource). It returns a flat, de-duplicated
// list of file paths, expanding any directories recursively.
function resolveUris(args: any[]): string[] {
    debugLog(`resolveUris: args=${JSON.stringify(args, (k, v) => v instanceof vscode.Uri ? v.toString() : v)}`);
    const uris: vscode.Uri[] = [];
    const collect = (item: any): void => {
        if (!item) {
            return;
        }
        if (item instanceof vscode.Uri) {
            uris.push(item);
        } else if (Array.isArray(item)) {
            for (const i of item) {
                collect(i);
            }
        } else if (typeof item === "object") {
            // SCM resource states (Git panel) expose `resourceUri`.
            if (item.resourceUri instanceof vscode.Uri) {
                uris.push(item.resourceUri);
            } else if (item.uri instanceof vscode.Uri) {
                uris.push(item.uri);
            } else if (item.input && item.input.uri instanceof vscode.Uri) {
                // Tab object passed by editor/title/context in some versions.
                uris.push(item.input.uri);
            }
        }
    };
    for (const arg of args) {
        collect(arg);
    }

    // Fall back to active editor if no URIs from the invoking context.
    if (uris.length === 0) {
        const active = vscode.window.activeTextEditor;
        if (active) {
            uris.push(active.document.uri);
        }
    }

    // Expand directories and collect files, de-duplicating by path.
    const filePaths: string[] = [];
    const seen = new Set<string>();
    for (const uri of uris) {
        const fsPath = uri.fsPath;
        if (seen.has(fsPath)) {
            continue;
        }
        try {
            const stat = fs.statSync(fsPath);
            if (stat.isDirectory()) {
                for (const f of expandDirectory(fsPath)) {
                    if (!seen.has(f)) {
                        seen.add(f);
                        filePaths.push(f);
                    }
                }
            } else if (stat.isFile()) {
                seen.add(fsPath);
                filePaths.push(fsPath);
            }
        } catch {
            // skip unreadable / non-file-scheme entries
        }
    }

    debugLog(`resolveUris: -> ${filePaths.length} files: ${JSON.stringify(filePaths)}`);
    return filePaths;
}

// runGit executes git in cwd and resolves with stdout. It prefers the path
// configured for VS Code's built-in git (git.path), falling back to "git" on
// PATH; on Windows it runs through a shell so git.cmd resolves.
function runGit(args: string[], cwd: string): Promise<string> {
    const configured = vscode.workspace.getConfiguration("git").get<string>("path");
    const git = configured && configured.length > 0 ? configured : "git";
    return new Promise((resolve, reject) => {
        execFile(git, args, {
            cwd,
            maxBuffer: 64 * 1024 * 1024,
            shell: process.platform === "win32",
        }, (err, stdout) => {
            if (err) {
                reject(err);
            } else {
                resolve(stdout);
            }
        });
    });
}

// collectGitChangedFiles returns absolute paths of files git reports as changed
// (modified, staged, added, untracked) in the repo at cwd. Deletions are skipped
// (the file no longer exists); renames resolve to whichever path still exists.
async function collectGitChangedFiles(cwd: string): Promise<string[]> {
    // Resolve the actual git repo root first - the workspace folder may be a
    // subdirectory of the repo, and `git status` without the right cwd yields
    // paths relative to the subdirectory which would be wrong.
    let gitRoot = cwd;
    try {
        const root = await runGit(["rev-parse", "--show-toplevel"], cwd);
        const trimmed = root.trim();
        if (trimmed.length > 0) {
            gitRoot = trimmed;
        }
    } catch {
        // not a git repo - let the status call below fail naturally
    }
    const out = await runGit(["status", "--porcelain=v1", "-z", "--untracked-files=all"], gitRoot);
    const files: string[] = [];
    const seen = new Set<string>();
    const tokens = out.split("\0");
    let i = 0;
    while (i < tokens.length) {
        const entry = tokens[i++];
        if (entry.length === 0) {
            continue;
        }
        // The primary path follows the "XY " status prefix.
        const candidates: string[] = [entry.substring(3)];
        // Renames/copies carry a second NUL-separated path (the source).
        const x = entry.charAt(0);
        const y = entry.charAt(1);
        if (x === "R" || x === "C" || y === "R" || y === "C") {
            candidates.push(tokens[i++]);
        }
        for (const rel of candidates) {
            if (!rel) {
                continue;
            }
            const full = path.isAbsolute(rel) ? rel : path.join(gitRoot, rel);
            if (seen.has(full)) {
                continue;
            }
            try {
                if (fs.statSync(full).isFile()) {
                    seen.add(full);
                    files.push(full);
                }
            } catch {
                // deleted or unreadable - skip
            }
        }
    }
    return files;
}

function showResult(result: UploadResponse, totalFiles: number) {
    debugLog(`showResult: totalFiles=${totalFiles} uploaded=${result.uploaded} failed=${result.failed} results=${JSON.stringify(result.results)}`);
    if (result.uploaded > 0 && result.failed === 0) {
        const word = result.uploaded === 1 ? "file" : "files";
        vscode.window.showInformationMessage(
            `SmartDeploy: ${result.uploaded} ${word} transferred`
        );
    } else if (result.uploaded > 0 && result.failed > 0) {
        vscode.window.showWarningMessage(
            `SmartDeploy: ${result.uploaded} uploaded, ${result.failed} failed`
        );
    } else if (result.failed > 0) {
        const err = result.results[0]?.error || "Unknown error";
        vscode.window.showErrorMessage(`SmartDeploy: ${result.failed} files failed - ${err}`);
    } else {
        // No files reported as uploaded or failed (e.g. empty results from
        // the server). Surface this so the user is never left with no
        // feedback at all.
        vscode.window.showWarningMessage(
            `SmartDeploy: uploaded ${result.uploaded}, failed ${result.failed} (no per-file results)`
        );
    }
}

export function activate(context: vscode.ExtensionContext) {
    // Upload from editor or explorer context menu (supports multi-select).
    const uploadCmd = vscode.commands.registerCommand("smartdeploy.uploadFile", async (...args: any[]) => {
        debugLog("command: uploadFile");
        const filePaths = resolveUris(args);
        if (filePaths.length === 0) {
            vscode.window.showWarningMessage("SmartDeploy: No file selected");
            return;
        }
        try {
            const result = await uploadFiles(filePaths);
            showResult(result, filePaths.length);
        } catch (err: any) {
            debugLog(`uploadFile error: ${err.message}`);
            vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
        }
    });

    // Upload the active editor file via command palette.
    const uploadActiveCmd = vscode.commands.registerCommand("smartdeploy.uploadActiveFile", async () => {
        debugLog("command: uploadActiveFile");
        const activeEditor = vscode.window.activeTextEditor;
        if (!activeEditor) {
            vscode.window.showWarningMessage("SmartDeploy: No active file");
            return;
        }
        const filePath = activeEditor.document.uri.fsPath;
        try {
            const result = await uploadSingleFile(filePath);
            showResult(result, 1);
        } catch (err: any) {
            debugLog(`uploadActiveFile error: ${err.message}`);
            vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
        }
    });

    // Upload all open files from the command palette.
    const uploadAllOpenCmd = vscode.commands.registerCommand("smartdeploy.uploadAllOpenFiles", async () => {
        debugLog("command: uploadAllOpenFiles");
        const filePaths: string[] = [];
        for (const group of vscode.window.tabGroups.all) {
            for (const tab of group.tabs) {
                debugLog(`tab: label=${tab.label} inputType=${tab.input?.constructor?.name}`);
                // Accept both text tabs and custom-editor tabs (image preview,
                // webview editors, etc.) so that opened images are uploaded too.
                const input: any = tab.input;
                if (input && input.uri instanceof vscode.Uri) {
                    filePaths.push(input.uri.fsPath);
                }
            }
        }
        debugLog(`uploadAllOpenFiles: collected ${filePaths.length} files`);
        if (filePaths.length === 0) {
            vscode.window.showWarningMessage("SmartDeploy: No open files");
            return;
        }
        try {
            const result = await uploadFiles(filePaths);
            showResult(result, filePaths.length);
        } catch (err: any) {
            vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
        }
    });

    // Upload every file git reports as changed across all workspace folders.
    // Reachable from the Source Control view title menu and the changed-file
    // right-click menu.
    const uploadGitChangedCmd = vscode.commands.registerCommand("smartdeploy.uploadGitChangedFiles", async () => {
        debugLog("command: uploadGitChangedFiles");
        const folders = vscode.workspace.workspaceFolders;
        if (!folders || folders.length === 0) {
            vscode.window.showWarningMessage("SmartDeploy: No workspace folder open");
            return;
        }
        const filePaths: string[] = [];
        const seen = new Set<string>();
        let foundRepo = false;
        for (const folder of folders) {
            const cwd = folder.uri.fsPath;
            try {
                const changed = await collectGitChangedFiles(cwd);
                foundRepo = true;
                debugLog(`git changed in ${cwd}: ${changed.length} files: ${JSON.stringify(changed)}`);
                for (const f of changed) {
                    if (!seen.has(f)) {
                        seen.add(f);
                        filePaths.push(f);
                    }
                }
            } catch {
                // not a git repo, or git unavailable - skip this folder
            }
        }
        if (!foundRepo) {
            vscode.window.showWarningMessage("SmartDeploy: No git repository found in the workspace");
            return;
        }
        if (filePaths.length === 0) {
            vscode.window.showInformationMessage("SmartDeploy: No changed files to upload");
            return;
        }
        try {
            const result = await uploadFiles(filePaths);
            showResult(result, filePaths.length);
        } catch (err: any) {
            debugLog(`uploadGitChangedFiles error: ${err.message}`);
            vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
        }
    });

    const statusCmd = vscode.commands.registerCommand("smartdeploy.checkStatus", async () => {
        try {
            const status = await checkStatus();
            const state = status.connected ? "Connected" : "Disconnected";
            const last = status.lastUpload ? ` | Last: ${status.lastUpload}` : "";
            vscode.window.showInformationMessage(
                `SmartDeploy: ${state} | Pending: ${status.pending}${last}`
            );
        } catch (err: any) {
            vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
        }
    });

    // Clear the temp staging directory on the server. Shows a strong
    // confirmation with the path being deleted to prevent accidents.
    const clearTempCmd = vscode.commands.registerCommand("smartdeploy.clearTemp", async () => {
        const clearPath = vscode.workspace.getConfiguration("smartdeploy").get<string>("clearPath", "");
        const pathLabel = clearPath || "the temp directory";
        const choice = await vscode.window.showWarningMessage(
            `SmartDeploy: Delete all files under '${pathLabel}' on the server? This cannot be undone.`,
            "Yes, Delete", "Cancel"
        );
        if (choice !== "Yes, Delete") return;
        await vscode.window.withProgress({
            location: vscode.ProgressLocation.Notification,
            title: "SmartDeploy: Clearing temp directory...",
            cancellable: false,
        }, async () => {
            try {
                await clearTemp();
                vscode.window.showInformationMessage("SmartDeploy: Temp directory cleared");
            } catch (err: any) {
                vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
            }
        });
    });

    // Rsync the temp directory to the webapp root.
    const syncCmd = vscode.commands.registerCommand("smartdeploy.sync", async () => {
        await vscode.window.withProgress({
            location: vscode.ProgressLocation.Notification,
            title: "SmartDeploy: Syncing to webapp...",
            cancellable: false,
        }, async () => {
            try {
                const output = await syncToWebapp();
                vscode.window.showInformationMessage("SmartDeploy: Sync complete");
                debugLog(`sync output: ${output}`);
            } catch (err: any) {
                vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
            }
        });
    });

    const autoUploadOnSave = vscode.workspace.onWillSaveTextDocument(async (event) => {
        const enabled = vscode.workspace.getConfiguration("smartdeploy").get<boolean>("autoUploadOnSave", false);
        if (!enabled) return;

        const filePath = event.document.uri.fsPath;
        try {
            const result = await uploadSingleFile(filePath);
            if (result.uploaded > 0) {
                const remote = result.results[0]?.remotePath || "";
                const fileName = path.basename(filePath);
                vscode.window.setStatusBarMessage(
                    `SmartDeploy: ${fileName} -> ${remote}`, 3000
                );
            }
        } catch (err: any) {
            // Silent on auto-upload to avoid noise
        }
    });

    context.subscriptions.push(uploadCmd, uploadActiveCmd, uploadAllOpenCmd, uploadGitChangedCmd, clearTempCmd, syncCmd, statusCmd, autoUploadOnSave);
}

export function deactivate() {}
