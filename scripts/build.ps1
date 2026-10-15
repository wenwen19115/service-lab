$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path $PSScriptRoot
Push-Location (Join-Path $taskRoot 'web')
try {
    & npm.cmd ci
    if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    & npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw 'UI build failed' }
} finally { Pop-Location }
Push-Location $taskRoot
try {
    $taskGo = 'go'
    $taskPortable = Join-Path $taskRoot '.tools\go\bin\go.exe'
    if (Test-Path $taskPortable) { $taskGo = $taskPortable }
    New-Item -ItemType Directory -Force -Path 'dist' | Out-Null
    & $taskGo test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    $taskBuildTime = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    & $taskGo build -ldflags "-H=windowsgui -X service-lab/internal/lab.BuildTime=$taskBuildTime" -o dist/service-lab.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
} finally { Pop-Location }
