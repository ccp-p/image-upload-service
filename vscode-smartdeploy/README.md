# SmartDeploy VSCode Extension

Upload files to your server via SmartDeploy, directly from VSCode.

## How it works

1. Start SmartDeploy (it listens on localhost:9721 by default)
2. Connect to your server (OTP as usual)
3. Right-click any file in VSCode (editor or explorer) -> SmartDeploy: Upload File
4. The file uploads immediately using the active SSH connection

No re-OTP needed. The extension talks to the already-running SmartDeploy process.

## Installation

### Option A: Package and install

Run in this folder:

  npm install
  npm run compile
  npm install -g @vscode/vsce
  vsce package
  code --install-extension smartdeploy-0.1.0.vsix

### Option B: Development mode

  npm install
  npm run compile
  Press F5 in VSCode to launch Extension Development Host

## Commands

- SmartDeploy: Upload File - Upload the right-clicked file (context menu)
- SmartDeploy: Upload Current File - Upload the active editor file (Ctrl+Shift+P)
- SmartDeploy: Check Connection Status - Show SmartDeploy connection info (Ctrl+Shift+P)

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| smartdeploy.apiPort | 9721 | Port SmartDeploy API listens on |
| smartdeploy.autoUploadOnSave | false | Auto-upload files on save |

## Requirements

- SmartDeploy must be running and connected to the server
- The API port must match your config.json apiPort setting

