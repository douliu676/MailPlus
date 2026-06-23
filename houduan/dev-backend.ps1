param(
  [string]$BackendDir = $PSScriptRoot,
  [string]$RunDir = (Join-Path $PSScriptRoot "temp\dev-backend")
)

$restartExitCode = 42
$exePath = Join-Path $RunDir "mail-dev-backend.exe"

if (!(Test-Path $RunDir)) {
  New-Item -ItemType Directory -Path $RunDir | Out-Null
}

while ($true) {
  Push-Location $BackendDir
  try {
    go build -o $exePath .
    $buildExitCode = $LASTEXITCODE
    if ($buildExitCode -ne 0) {
      exit $buildExitCode
    }
    & $exePath
    $exitCode = $LASTEXITCODE
  } finally {
    Pop-Location
  }

  if ($exitCode -eq $restartExitCode) {
    Write-Host "Database restore completed, restarting backend..."
    Start-Sleep -Seconds 1
    continue
  }

  exit $exitCode
}
