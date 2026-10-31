<#
.SYNOPSIS
  Reconstruye DByson desde este checkout y reinicia el contenedor local con la versión nueva.

.DESCRIPTION
  1. Construye la imagen (docker build) con el código actual, incluidos los cambios sin commit.
  2. Respalda el volumen de datos en %USERPROFILE%\dbyson-backups (salvo -SkipBackup).
  3. Recrea el contenedor con el mismo volumen, puerto y variables DBYSON_* (clave maestra
     incluida, sin mostrarlas). El contenedor anterior queda detenido como <nombre>-previous
     para volver atrás:  docker rm -f dbyson; docker rename dbyson-previous dbyson; docker start dbyson

.EXAMPLE
  .\scripts\redeploy-local.ps1
  .\scripts\redeploy-local.ps1 -SkipBackup
#>
param(
  [string]$Container = "dbyson",
  [string]$Image = "dbyson:dev",
  [int]$Port = 8083,
  [switch]$SkipBackup
)
$ErrorActionPreference = "Stop"
Set-Location (Split-Path $PSScriptRoot -Parent)

function Invoke-Docker {
  docker @args
  if ($LASTEXITCODE -ne 0) { throw "docker $($args[0]) falló (código $LASTEXITCODE)" }
}

$current = docker inspect $Container 2>$null | ConvertFrom-Json
if (-not $current) { throw "No existe el contenedor '$Container'. Créalo una vez con docker run antes de usar este script." }
$current = $current[0]
$envs = @($current.Config.Env | Where-Object { $_ -match '^DBYSON_' })
$volume = ($current.Mounts | Where-Object { $_.Destination -eq '/data' }).Name
if (-not $volume) { throw "El contenedor '$Container' no monta un volumen en /data." }

Write-Host "==> Construyendo $Image con el código de $(Get-Location)…"
Invoke-Docker build -t $Image .

if (-not $SkipBackup) {
  $backups = Join-Path $env:USERPROFILE "dbyson-backups"
  New-Item -ItemType Directory -Force $backups | Out-Null
  $file = "$volume-$(Get-Date -Format yyyyMMdd-HHmmss).tgz"
  Write-Host "==> Respaldando el volumen $volume en $backups\$file…"
  Invoke-Docker run --rm -v "${volume}:/data:ro" -v "${backups}:/backup" alpine:3.20 tar czf "/backup/$file" -C /data .
}

$previous = "$Container-previous"
docker rm -f $previous 2>$null | Out-Null
Write-Host "==> Deteniendo $Container (queda como $previous)…"
Invoke-Docker stop $Container | Out-Null
Invoke-Docker rename $Container $previous

$runArgs = @("run", "-d", "--name", $Container, "-p", "127.0.0.1:${Port}:8080", "-v", "${volume}:/data")
foreach ($e in $envs) { $runArgs += @("-e", $e) }
$runArgs += $Image
docker @runArgs | Out-Null
if ($LASTEXITCODE -ne 0) {
  Write-Warning "No se pudo iniciar la versión nueva; restaurando la anterior."
  docker rm -f $Container 2>$null | Out-Null
  docker rename $previous $Container
  docker start $Container | Out-Null
  throw "docker run falló"
}

Write-Host "==> Esperando a que responda http://localhost:$Port…"
for ($i = 0; $i -lt 30; $i++) {
  try {
    if ((Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://127.0.0.1:$Port/api/status").StatusCode -eq 200) {
      Write-Host "Listo: http://localhost:$Port  (versión anterior detenida como $previous)" -ForegroundColor Green
      return
    }
  } catch { Start-Sleep -Seconds 1 }
}
Write-Warning "El contenedor arrancó pero no responde todavía. Revisa: docker logs $Container"
