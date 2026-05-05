# Laevitas CLI installer for Windows.
#
# Usage:
#   irm https://raw.githubusercontent.com/laevitas/cli/main/install.ps1 | iex
#
# Environment overrides:
#   $env:LAEVITAS_VERSION  — install a specific version (e.g. v0.5.0). Defaults to latest.
#   $env:LAEVITAS_PREFIX   — install directory. Defaults to $env:USERPROFILE\bin.

$ErrorActionPreference = 'Stop'

$Repo    = 'laevitas/cli'
$BinName = 'laevitas.exe'

function Write-Info($msg) { Write-Host "→ $msg" -ForegroundColor Cyan }
function Write-Ok($msg)   { Write-Host "✓ $msg" -ForegroundColor Green }
function Write-Err($msg)  { Write-Host "✗ $msg" -ForegroundColor Red; exit 1 }

# ─── detect arch ─────────────────────────────────────────────────────────────
$osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
$arch = switch ($osArch) {
    'X64'   { 'x86_64' }
    'Arm64' { 'arm64' }
    default { Write-Err "Unsupported architecture: $osArch" }
}

# ─── pick version ────────────────────────────────────────────────────────────
$version = if ($env:LAEVITAS_VERSION) { $env:LAEVITAS_VERSION } else {
    Write-Info 'Resolving latest release...'
    try {
        $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
        $rel.tag_name
    } catch {
        Write-Err "Could not determine latest release tag: $_"
    }
}
$versionBare = $version.TrimStart('v')

# ─── pick install dir ────────────────────────────────────────────────────────
$prefix = if ($env:LAEVITAS_PREFIX) { $env:LAEVITAS_PREFIX } else { Join-Path $env:USERPROFILE 'bin' }
if (-not (Test-Path $prefix)) { New-Item -ItemType Directory -Path $prefix -Force | Out-Null }

# ─── download + extract ──────────────────────────────────────────────────────
$archive = "laevitas_${versionBare}_Windows_${arch}.zip"
$url     = "https://github.com/$Repo/releases/download/$version/$archive"

Write-Info "Downloading $url"
$tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "laevitas-$([guid]::NewGuid())") -Force
try {
    $archivePath = Join-Path $tmp $archive
    try {
        Invoke-WebRequest -Uri $url -OutFile $archivePath -UseBasicParsing
    } catch {
        Write-Err "Download failed. Check that $version exists at https://github.com/$Repo/releases"
    }

    # Verify checksum — fail closed on every error path.
    $sumsUrl  = "https://github.com/$Repo/releases/download/$version/checksums.txt"
    $sumsPath = Join-Path $tmp 'checksums.txt'
    try {
        Invoke-WebRequest -Uri $sumsUrl -OutFile $sumsPath -UseBasicParsing
    } catch {
        Write-Err "Failed to download checksums.txt from $sumsUrl — refusing to install without integrity verification."
    }

    # Tolerate both GoReleaser line formats: text-mode "<hash>  <name>" and binary-mode "<hash>  *<name>".
    $escaped = [regex]::Escape($archive)
    $line = (Get-Content $sumsPath | Where-Object { $_ -match "\s\*?$escaped$" } | Select-Object -First 1)
    if (-not $line) {
        Write-Err "Checksum line for $archive not found in checksums.txt — refusing to install."
    }

    $expected = ($line -split '\s+')[0].ToLower()
    $actual   = (Get-FileHash -Algorithm SHA256 -Path $archivePath).Hash.ToLower()
    if ($expected -ne $actual) {
        Write-Err "Checksum mismatch for ${archive}: expected $expected, got $actual"
    }
    Write-Info "Checksum verified ($actual)."

    Expand-Archive -Path $archivePath -DestinationPath $tmp -Force
    $binSrc = Join-Path $tmp $BinName
    if (-not (Test-Path $binSrc)) { Write-Err "Archive did not contain expected binary '$BinName'." }

    $binDst = Join-Path $prefix $BinName
    Copy-Item -Path $binSrc -Destination $binDst -Force
} finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Ok "Installed $BinName $version → $binDst"

# Warn / offer to add prefix to PATH.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not ($userPath -split ';' | Where-Object { $_ -ieq $prefix })) {
    Write-Host ''
    Write-Host "! $prefix is not on your PATH." -ForegroundColor Yellow
    Write-Host '  Adding it to your User PATH (takes effect in new terminals)...'
    $newPath = if ($userPath) { "$userPath;$prefix" } else { $prefix }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:Path = "$env:Path;$prefix"
    Write-Ok 'PATH updated.'
}

& $binDst version
