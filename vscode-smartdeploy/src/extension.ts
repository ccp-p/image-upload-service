import * as vscode from "vscode";
import * as http from "http";
import * as path from "path";
import * as fs from "fs";
import { execFile } from "child_process";

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

function uploadFiles(filePaths: string[]): Promise<UploadResponse> {
    return new Promise((resolve, reject) => {
        const url = getApiUrl() + "/upload";
        const body = JSON.stringify({ paths: filePaths });

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
                if (res.statusCode === undefined) {
                    reject(new Error("No status code"));
                    return;
                }
                try {
                    const parsed = JSON.parse(data);
                    if (res.statusCode >= 200 && res.statusCode < 300) {
                        resolve(parsed as UploadResponse);
                    } else {
                        const errMsg = (parsed as any).error || `HTTP ${res.statusCode}`;
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
    const out = await runGit(["status", "--porcelain=v1", "-z", "--untracked-files=all"], cwd);
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
            const full = path.isAbsolute(rel) ? rel : path.join(cwd, rel);
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
    if (result.uploaded > 0 && result.failed === 0) {
        const label = totalFiles === 1
            ? path.basename(result.results[0]?.localPath || "")
            : `${result.uploaded} files`;
        const remote = result.results[0]?.remotePath || "";
        vscode.window.showInformationMessage(
            `SmartDeploy: ${label} uploaded${remote && totalFiles === 1 ? " -> " + remote : ""}`
        );
    } else if (result.uploaded > 0 && result.failed > 0) {
        vscode.window.showWarningMessage(
            `SmartDeploy: ${result.uploaded} uploaded, ${result.failed} failed`
        );
    } else if (result.failed > 0) {
        const err = result.results[0]?.error || "Unknown error";
        vscode.window.showErrorMessage(`SmartDeploy: ${result.failed} files failed - ${err}`);
    }
}

export function activate(context: vscode.ExtensionContext) {
    // Upload from editor or explorer context menu (supports multi-select).
    const uploadCmd = vscode.commands.registerCommand("smartdeploy.uploadFile", async (...args: any[]) => {
        const filePaths = resolveUris(args);
        if (filePaths.length === 0) {
            vscode.window.showWarningMessage("SmartDeploy: No file selected");
            return;
        }
        try {
            const result = await uploadFiles(filePaths);
            showResult(result, filePaths.length);
        } catch (err: any) {
            vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
        }
    });

    // Upload the active editor file via command palette.
    const uploadActiveCmd = vscode.commands.registerCommand("smartdeploy.uploadActiveFile", async () => {
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
            vscode.window.showErrorMessage(`SmartDeploy: ${err.message}`);
        }
    });

    // Upload all open files from the command palette.
    const uploadAllOpenCmd = vscode.commands.registerCommand("smartdeploy.uploadAllOpenFiles", async () => {
        const filePaths: string[] = [];
        for (const group of vscode.window.tabGroups.all) {
            for (const tab of group.tabs) {
                if (tab.input instanceof vscode.TabInputText) {
                    filePaths.push(tab.input.uri.fsPath);
                }
            }
        }
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

    context.subscriptions.push(uploadCmd, uploadActiveCmd, uploadAllOpenCmd, uploadGitChangedCmd, statusCmd, autoUploadOnSave);
}

export function deactivate() {}
