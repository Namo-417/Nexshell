[CmdletBinding()]
param(
    [string]$CompilerPath = (Join-Path $PSScriptRoot '..\.tools\llvm-mingw-20260908-ucrt-x86_64\bin\clang.exe')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# Use a project-local or explicitly supplied compiler. Do not change the
# machine/user PATH or Go configuration, or download a compiler from this script.
if (-not (Test-Path -LiteralPath $CompilerPath -PathType Leaf)) {
    throw "Race compiler not found: $CompilerPath. Supply a 64-bit MinGW-w64 compiler with -CompilerPath."
}
$compiler = (Resolve-Path -LiteralPath $CompilerPath).Path
$compilerDirectory = Split-Path -Parent $compiler
$repositoryRoot = Split-Path -Parent $PSScriptRoot

$savedEnvironment = @{}
foreach ($name in @('CC', 'CGO_ENABLED', 'PATH')) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}

$testExitCode = 1
Push-Location -LiteralPath $repositoryRoot
try {
    # Keep the compiler's runtime DLLs ahead of unrelated MinGW installations.
    $env:PATH = $compilerDirectory + [IO.Path]::PathSeparator + $env:PATH
    $target = & $compiler -dumpmachine
    if ($LASTEXITCODE -ne 0 -or $target -notmatch '^x86_64-.*(windows|mingw)') {
        throw "Expected a 64-bit Windows compiler; got: $target"
    }
    $synchronization = & $compiler --print-file-name libsynchronization.a
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $synchronization -PathType Leaf)) {
        throw 'The compiler needs MinGW-w64 8+ (libsynchronization.a) for the Go race runtime.'
    }

    # Go accepts a quoted executable in CC, including compiler paths with spaces.
    $env:CC = '"' + $compiler + '"'
    $env:CGO_ENABLED = '1'
    Write-Host "Race compiler: $compiler"
    & go test -race -count=1 -timeout=5m ./...
    $testExitCode = $LASTEXITCODE
}
finally {
    foreach ($name in $savedEnvironment.Keys) {
        if ($null -eq $savedEnvironment[$name]) {
            [Environment]::SetEnvironmentVariable($name, [NullString]::Value, 'Process')
        }
        else {
            [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process')
        }
    }
    Pop-Location
}

exit $testExitCode
