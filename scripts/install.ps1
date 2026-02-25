# Laevitas CLI installer for Windows
# Usage: irm https://cli.laevitas.ch/install.ps1 | iex
#
# Detects architecture, downloads the latest release, installs to LOCALAPPDATA

$ErrorActionPreference = "Stop"

$Repo = "laevitas/cli"
$BinaryName = "laevitas"
$InstallDir = Join-Path $env:LOCALAPPDATA "laevitas"

function Write-Info($msg)  { Write-Host "▸ $msg" -ForegroundColor Green }
function Write-Warn($msg)  { Write-Host "▸ $msg" -ForegroundColor Yellow }
function Write-Err($msg)   { Write-Host "▸ $msg" -ForegroundColor Red; exit 1 }

# Detect architecture
function Get-Arch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    switch ($arch) {
        "X64"   { return "amd64" }
        "Arm64" { return "arm64" }
        default { Write-Err "Unsupported architecture: $arch" }
    }
}

function Main {
    $arch = Get-Arch
    Write-Info "Detected: windows/$arch"

    # Get latest release tag
    Write-Info "Fetching latest version..."
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
        $tag = $release.tag_name
    } catch {
        Write-Err "Could not determine latest version. Check your internet connection."
    }

    if (-not $tag) {
        Write-Err "Could not determine latest version"
    }

    Write-Info "Latest version: $tag"

    # Build download URL
    $fileName = "$BinaryName-windows-${arch}.exe"
    $downloadUrl = "https://github.com/$Repo/releases/download/$tag/$fileName"

    # Create install directory
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    $destPath = Join-Path $InstallDir "$BinaryName.exe"

    # Download
    Write-Info "Downloading $downloadUrl..."
    try {
        Invoke-WebRequest -Uri $downloadUrl -OutFile $destPath -UseBasicParsing
    } catch {
        Write-Err "Download failed: $_"
    }

    # Add to PATH if not already there
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$InstallDir*") {
        Write-Info "Adding $InstallDir to user PATH..."
        [Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
        $env:Path = "$env:Path;$InstallDir"
    }

    Write-Info "Installed $BinaryName $tag to $destPath"
    Write-Host ""
    Write-Info "Get started:"
    Write-Host "  $BinaryName config init          # Set up your API key"
    Write-Host "  $BinaryName futures snapshot      # Your first query"
    Write-Host "  $BinaryName --help                # See all commands"
    Write-Host ""
    Write-Warn "Restart your terminal for PATH changes to take effect."
}

Main
