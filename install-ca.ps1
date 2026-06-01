# 安装 CA 证书到 Windows 系统信任证书库
# 需要以管理员权限运行

$ErrorActionPreference = "Stop"

$caPath = Join-Path $env:USERPROFILE ".my-claude-proxy\ca.crt"

if (-not (Test-Path $caPath)) {
    Write-Host "[ERROR] CA 证书文件未找到: $caPath" -ForegroundColor Red
    Write-Host "请先运行 proxy.exe 以生成 CA 证书" -ForegroundColor Yellow
    exit 1
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  安装 CA 证书到系统信任库" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "证书路径: $caPath" -ForegroundColor White

# Check if running as admin
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")

if ($isAdmin) {
    # Install to LocalMachine (system-wide)
    try {
        $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($caPath)
        $store = New-Object System.Security.Cryptography.X509Certificates.X509Store("Root", "LocalMachine")
        $store.Open("ReadWrite")
        
        # Check if already installed
        $existing = $store.Certificates | Where-Object { $_.Thumbprint -eq $cert.Thumbprint }
        if ($existing) {
            Write-Host ""
            Write-Host "  ✓ CA 证书已存在于系统信任库中" -ForegroundColor Green
        } else {
            $store.Add($cert)
            Write-Host ""
            Write-Host "  ✓ CA 证书已安装到系统信任库 (LocalMachine\Root)" -ForegroundColor Green
        }
        
        $store.Close()
        Write-Host ""
        Write-Host "证书信息:" -ForegroundColor Cyan
        Write-Host "  Subject:    $($cert.Subject)" -ForegroundColor White
        Write-Host "  Thumbprint: $($cert.Thumbprint)" -ForegroundColor White
        Write-Host "  NotBefore:  $($cert.NotBefore)" -ForegroundColor White
        Write-Host "  NotAfter:   $($cert.NotAfter)" -ForegroundColor White
    } catch {
        Write-Host "[ERROR] 安装失败: $_" -ForegroundColor Red
        exit 1
    }
} else {
    # Install to CurrentUser (no admin needed)
    try {
        $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($caPath)
        $store = New-Object System.Security.Cryptography.X509Certificates.X509Store("Root", "CurrentUser")
        $store.Open("ReadWrite")
        
        $existing = $store.Certificates | Where-Object { $_.Thumbprint -eq $cert.Thumbprint }
        if ($existing) {
            Write-Host ""
            Write-Host "  ✓ CA 证书已存在于用户信任库中" -ForegroundColor Green
        } else {
            $store.Add($cert)
            Write-Host ""
            Write-Host "  ✓ CA 证书已安装到用户信任库 (CurrentUser\Root)" -ForegroundColor Green
            Write-Host ""
            Write-Host "  [TIP] 如需安装到系统级信任库,请以管理员身份重新运行此脚本" -ForegroundColor Yellow
        }
        
        $store.Close()
        Write-Host ""
        Write-Host "证书信息:" -ForegroundColor Cyan
        Write-Host "  Subject:    $($cert.Subject)" -ForegroundColor White
        Write-Host "  Thumbprint: $($cert.Thumbprint)" -ForegroundColor White
        Write-Host "  NotBefore:  $($cert.NotBefore)" -ForegroundColor White
        Write-Host "  NotAfter:   $($cert.NotAfter)" -ForegroundColor White
    } catch {
        Write-Host "[ERROR] 安装失败: $_" -ForegroundColor Red
        exit 1
    }
}

Write-Host ""
