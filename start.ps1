# My Claude Proxy — 一键启动脚本
# 使用前请先设置您的 Anthropic API Key

param(
    [string]$ApiKey = $env:ANTHROPIC_API_KEY,
    [string]$ProxyPort = "9090",
    [string]$BackendPort = "8080",
    [string]$AnthropicUrl = "https://api.anthropic.com"
)

$ErrorActionPreference = "Stop"
$ProjectDir = $PSScriptRoot

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  My Claude Proxy — 一键启动" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# Check Go installation
$goPath = "C:\Program Files\Go\bin\go.exe"
if (-not (Test-Path $goPath)) {
    # Try system PATH
    try {
        $goPath = (Get-Command go -ErrorAction Stop).Source
    } catch {
        Write-Host "[ERROR] Go 未安装! 请先安装 Go: https://go.dev/dl/" -ForegroundColor Red
        exit 1
    }
}
Write-Host "[OK] Go found: $goPath" -ForegroundColor Green

# Check API key
if ([string]::IsNullOrEmpty($ApiKey)) {
    Write-Host ""
    Write-Host "[WARNING] 未设置 API Key!" -ForegroundColor Yellow
    Write-Host "  请通过以下方式之一设置:" -ForegroundColor Yellow
    Write-Host "  1. 环境变量: `$env:ANTHROPIC_API_KEY = 'sk-ant-...'`" -ForegroundColor Yellow
    Write-Host "  2. 命令行参数: .\start.ps1 -ApiKey 'sk-ant-...'" -ForegroundColor Yellow
    Write-Host ""
    $ApiKey = Read-Host "请输入你的 Anthropic API Key (或按 Enter 跳过)"
}

if (-not [string]::IsNullOrEmpty($ApiKey)) {
    $masked = $ApiKey.Substring(0, [Math]::Min(10, $ApiKey.Length)) + "..."
    Write-Host "[OK] API Key: $masked" -ForegroundColor Green
}

# Step 1: Build the projects
Write-Host ""
Write-Host "[1/5] 编译项目..." -ForegroundColor Cyan

Push-Location $ProjectDir
& $goPath build -o "$ProjectDir\proxy.exe" ./cmd/proxy/
if ($LASTEXITCODE -ne 0) {
    Write-Host "[ERROR] 编译 proxy 失败!" -ForegroundColor Red
    Pop-Location
    exit 1
}
Write-Host "  ✓ proxy.exe 编译完成" -ForegroundColor Green

& $goPath build -o "$ProjectDir\backend.exe" ./cmd/backend/
if ($LASTEXITCODE -ne 0) {
    Write-Host "[ERROR] 编译 backend 失败!" -ForegroundColor Red
    Pop-Location
    exit 1
}
Write-Host "  ✓ backend.exe 编译完成" -ForegroundColor Green
Pop-Location

# Step 2: Install CA certificate
Write-Host ""
Write-Host "[2/5] 初始化 CA 证书..." -ForegroundColor Cyan
# Run proxy briefly to generate CA cert if needed, then stop
$tempProcess = Start-Process -FilePath "$ProjectDir\proxy.exe" -ArgumentList "-listen 127.0.0.1:0" -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2
if (-not $tempProcess.HasExited) {
    Stop-Process -Id $tempProcess.Id -Force -ErrorAction SilentlyContinue
}

$caPath = Join-Path $env:USERPROFILE ".my-claude-proxy\ca.crt"
if (Test-Path $caPath) {
    Write-Host "  ✓ CA 证书已生成: $caPath" -ForegroundColor Green
    Write-Host ""
    Write-Host "[3/5] 安装 CA 证书到系统信任库..." -ForegroundColor Cyan
    & powershell -File "$ProjectDir\install-ca.ps1"
} else {
    Write-Host "  [WARNING] CA 证书未找到,稍后手动安装" -ForegroundColor Yellow
}

# Step 3: Start backend server
Write-Host ""
Write-Host "[4/5] 启动后端服务 (端口 $BackendPort)..." -ForegroundColor Cyan
$backendArgs = "-listen 127.0.0.1:$BackendPort -anthropic-url $AnthropicUrl"
if (-not [string]::IsNullOrEmpty($ApiKey)) {
    $backendArgs += " -api-key $ApiKey"
}
$backendProcess = Start-Process -FilePath "$ProjectDir\backend.exe" -ArgumentList $backendArgs -PassThru -NoNewWindow
Write-Host "  ✓ 后端服务已启动 (PID: $($backendProcess.Id))" -ForegroundColor Green

Start-Sleep -Seconds 1

# Step 4: Start MITM proxy
Write-Host ""
Write-Host "[5/5] 启动 MITM 代理 (端口 $ProxyPort)..." -ForegroundColor Cyan
$proxyProcess = Start-Process -FilePath "$ProjectDir\proxy.exe" -ArgumentList "-listen 127.0.0.1:$ProxyPort -backend 127.0.0.1:$BackendPort" -PassThru -NoNewWindow
Write-Host "  ✓ MITM 代理已启动 (PID: $($proxyProcess.Id))" -ForegroundColor Green

Start-Sleep -Seconds 1

# Step 5: Print usage instructions
Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host "  ✓ 所有服务已启动!" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green
Write-Host ""
Write-Host "现在请在新的终端中运行以下命令来使用 Claude Code:" -ForegroundColor Yellow
Write-Host ""
Write-Host '  $env:HTTPS_PROXY = "http://127.0.0.1:' -NoNewline -ForegroundColor White
Write-Host "$ProxyPort" -NoNewline -ForegroundColor Cyan
Write-Host '"' -ForegroundColor White
Write-Host '  $env:NODE_TLS_REJECT_UNAUTHORIZED = "0"' -ForegroundColor White
Write-Host '  claude' -ForegroundColor White
Write-Host ""
Write-Host "按 Ctrl+C 停止所有服务" -ForegroundColor Gray
Write-Host ""

# Wait for Ctrl+C
try {
    while ($true) {
        Start-Sleep -Seconds 1
        # Check if processes are still running
        if ($backendProcess.HasExited) {
            Write-Host "[WARNING] 后端服务已退出 (ExitCode: $($backendProcess.ExitCode))" -ForegroundColor Yellow
        }
        if ($proxyProcess.HasExited) {
            Write-Host "[WARNING] MITM 代理已退出 (ExitCode: $($proxyProcess.ExitCode))" -ForegroundColor Yellow
        }
        if ($backendProcess.HasExited -and $proxyProcess.HasExited) {
            Write-Host "[INFO] 所有服务已退出" -ForegroundColor Yellow
            break
        }
    }
} finally {
    Write-Host ""
    Write-Host "正在停止服务..." -ForegroundColor Cyan
    if (-not $backendProcess.HasExited) {
        Stop-Process -Id $backendProcess.Id -Force -ErrorAction SilentlyContinue
        Write-Host "  ✓ 后端服务已停止" -ForegroundColor Green
    }
    if (-not $proxyProcess.HasExited) {
        Stop-Process -Id $proxyProcess.Id -Force -ErrorAction SilentlyContinue
        Write-Host "  ✓ MITM 代理已停止" -ForegroundColor Green
    }
    Write-Host "所有服务已清理完毕。" -ForegroundColor Green
}
