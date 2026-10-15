[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$toolchainName = 'llvm-mingw-20260908-ucrt-x86_64'
$archiveName = "$toolchainName.zip"
$downloadUrl = "https://github.com/mstorsjo/llvm-mingw/releases/download/20260908/$archiveName"
$expectedHash = '1bcf74d06b724aeecaa6412ca85f5b26fb1da770e7cdcefa9263c9c5c3ad34b6'
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$toolsDirectory = Join-Path $repositoryRoot '.tools'
$toolchainDirectory = Join-Path $toolsDirectory $toolchainName
$compiler = Join-Path $toolchainDirectory 'bin\clang.exe'
$archive = Join-Path $toolsDirectory $archiveName

# Reuse the portable installation, including while another terminal runs tests.
# An existing incomplete directory must never be overlaid with new binaries.
if (Test-Path -LiteralPath $compiler -PathType Leaf) {
    Write-Host "Portable race compiler is already installed: $compiler"
    return
}
if (Test-Path -LiteralPath $toolchainDirectory) {
    throw "Incomplete toolchain directory exists: $toolchainDirectory. Move it aside before retrying; no files were overwritten."
}

New-Item -ItemType Directory -Path $toolsDirectory -Force | Out-Null
if (-not (Test-Path -LiteralPath $archive -PathType Leaf)) {
    # A unique temporary name leaves existing downloads untouched. Keep a failed
    # download for inspection, and never extract until its checksum is verified.
    $download = Join-Path $toolsDirectory ($archiveName + '.' + [Guid]::NewGuid().ToString('N') + '.download')
    Write-Host "Downloading pinned LLVM-MinGW 20260908 (about 182 MiB) into $toolsDirectory"
    try {
        Invoke-WebRequest -Uri $downloadUrl -OutFile $download
    }
    catch {
        throw "Compiler download failed: $($_.Exception.Message). Any partial download remains at: $download"
    }
    $actualHash = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualHash -cne $expectedHash) {
        throw "Compiler download checksum mismatch. Expected $expectedHash; got $actualHash. Nothing was extracted. File: $download"
    }
    Move-Item -LiteralPath $download -Destination $archive
}

# Verify cached archives as well as new downloads before extracting anything.
$actualHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualHash -cne $expectedHash) {
    throw "Cached compiler archive checksum mismatch. Expected $expectedHash; got $actualHash. Nothing was extracted. Move the archive aside before retrying: $archive"
}
try {
    Expand-Archive -LiteralPath $archive -DestinationPath $toolsDirectory
}
catch {
    throw "Compiler extraction failed: $($_.Exception.Message). Inspect $toolchainDirectory before retrying; existing files will not be overwritten."
}
if (-not (Test-Path -LiteralPath $compiler -PathType Leaf)) {
    throw "Extraction completed without the expected compiler: $compiler"
}

Write-Host "Installed verified portable race compiler: $compiler"
Write-Host 'Run .\scripts\test-race.ps1 to use it. System PATH and Go settings were not changed.'
