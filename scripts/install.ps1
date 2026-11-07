# Install the jot CLI on Windows from a GitHub release.
#
#   irm https://raw.githubusercontent.com/ArpitK24/jot/main/scripts/install.ps1 | iex
#
# Environment:
#   JOT_VERSION      version to install, e.g. 0.3.0 (default: newest v* release)
#   JOT_INSTALL_DIR  where to put jot.exe (default: %LOCALAPPDATA%\jot\bin)
#
# It downloads jot_<version>_windows_<arch>.zip and checksums.txt from the
# release, checks the zip's SHA-256, copies jot.exe into place and adds that
# folder to your user PATH. For the desktop app, use the setup .exe from a
# "jot desktop" release instead.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repo = 'ArpitK24/jot'
$Api = if ($env:JOT_RELEASES_API) { $env:JOT_RELEASES_API } else { "https://api.github.com/repos/$Repo/releases" }
$Dl = if ($env:JOT_DOWNLOAD_URL) { $env:JOT_DOWNLOAD_URL } else { "https://github.com/$Repo/releases/download" }

function Fail($msg) {
    Write-Host "jot install: $msg" -ForegroundColor Red
    throw "jot install failed"
}

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Fail "unsupported CPU $($env:PROCESSOR_ARCHITECTURE)" }
}
# 32-bit PowerShell on 64-bit Windows reports x86; use the real OS CPU.
if ($env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $arch = 'arm64' } elseif ($env:PROCESSOR_ARCHITEW6432 -eq 'AMD64') { $arch = 'amd64' }

$version = "$env:JOT_VERSION".TrimStart('v')
if (-not $version) {
    # Desktop app releases are tagged desktop/v*, so take the newest v* tag
    # rather than /releases/latest.
    try {
        $releases = Invoke-RestMethod -Uri "$Api`?per_page=30" -Headers @{ 'User-Agent' = 'jot-install' }
    } catch { Fail "could not reach the GitHub API: $_" }
    $tag = $releases | Where-Object { $_.tag_name -match '^v\d' -and -not $_.draft -and -not $_.prerelease } |
        Select-Object -First 1 -ExpandProperty tag_name
    if (-not $tag) { Fail "could not find a v* release of $Repo" }
    $version = $tag.TrimStart('v')
}

$name = "jot_${version}_windows_${arch}"
Write-Host "Installing jot $version (windows/$arch)"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("jot-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp "$name.zip"
    $sums = Join-Path $tmp 'checksums.txt'
    try { Invoke-WebRequest -UseBasicParsing -Uri "$Dl/v$version/$name.zip" -OutFile $zip } catch { Fail "could not download $name.zip from release v$version" }
    try { Invoke-WebRequest -UseBasicParsing -Uri "$Dl/v$version/checksums.txt" -OutFile $sums } catch { Fail "could not download checksums.txt from release v$version" }

    $want = $null
    foreach ($line in Get-Content $sums) {
        $f = $line -split '\s+', 2
        if ($f.Count -eq 2 -and $f[1].TrimStart('*') -eq "$name.zip") { $want = $f[0].ToLower() }
    }
    if (-not $want) { Fail "$name.zip is not listed in checksums.txt" }
    $got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLower()
    if ($want -ne $got) { Fail "checksum mismatch for $name.zip (want $want, got $got); not installing" }
    Write-Host 'Checksum OK'

    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $exe = Join-Path $tmp "$name\jot.exe"
    if (-not (Test-Path $exe)) { Fail "archive has no $name\jot.exe" }

    $dir = if ($env:JOT_INSTALL_DIR) { $env:JOT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'jot\bin' }
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $dest = Join-Path $dir 'jot.exe'
    try { Copy-Item -Force $exe $dest } catch { Fail "could not write $dest (is jot running? close your AI tools and try again)" }
    $installed = try { & $dest version } catch { "jot $version" }
    Write-Host "Installed $installed to $dest"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $parts = @($userPath -split ';' | Where-Object { $_ })
    if ($parts -notcontains $dir) {
        [Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';'), 'User')
        $env:Path = "$env:Path;$dir"
        Write-Host "Added $dir to your user PATH. Open a new terminal to use jot there."
    }

    Write-Host ''
    Write-Host 'Next: connect your AI tools (it shows every change and asks first):'
    Write-Host '  jot setup'
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
