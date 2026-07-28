import * as vscode from "vscode";
import * as http from "http";
import * as path from "path";
import * as fs from "fs";

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

// resolveUris accepts the arguments VSCode passes to an explorer/editor
// context menu command. These can be: a single Uri, an array of Uris,
// or undefined (fall back to the active editor). It returns a flat list
// of file paths, expanding any directories recursively.
function resolveUris(args: any[]): string[] {
    const uris: vscode.Uri[] = [];
    for (const arg of args) {
        if (arg instanceof vscode.Uri) {
            uris.push(arg);
        } else if (Array.isArray(arg)) {
            for (const a of arg) {
                if (a instanceof vscode.Uri) {
                    uris.push(a);
                }
            }
        }
    }

    // Fall back to active editor if no URIs from explorer.
    if (uris.length === 0) {
        const active = vscode.window.activeTextEditor;
        if (active) {
            uris.push(active.document.uri);
        }
    }

    // Expand directories and collect files.
    const filePaths: string[] = [];
    for (const uri of uris) {
        const fsPath = uri.fsPath;
        try {
            const stat = fs.statSync(fsPath);
            if (stat.isDirectory()) {
                filePaths.push(...expandDirectory(fsPath));
            } else if (stat.isFile()) {
                filePaths.push(fsPath);
            }
        } catch {
            // skip unreadable
        }
    }

    return filePaths;
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

    context.subscriptions.push(uploadCmd, uploadActiveCmd, uploadAllOpenCmd, statusCmd, autoUploadOnSave);
}

export function deactivate() {}
