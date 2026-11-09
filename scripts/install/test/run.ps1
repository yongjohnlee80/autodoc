# scripts/install/test/run.ps1: the Windows installer's cells (docs/decisions/install.md#windows).
#
# Each cell runs install-autodoc.ps1 or update.ps1 against a fake release made here (a file:// base
# URL: the scripts' -BaseUrl), with LOCALAPPDATA and APPDATA in a fresh folder, and checks what they
# did on disk. The fake autodoc.exe is a small Go program that answers --version.
#
#   powershell -ExecutionPolicy Bypass -File scripts/install/test/run.ps1   (from the repo's root; needs go)
#
# It prints a PASS or FAIL line per cell and "installer cells: PASS=n FAIL=m"; its exit status is the
# number of failures (0 when all pass).

$ErrorActionPreference = "Stop"
$scripts = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) ("autodoc-installer-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$script:pass = 0
$script:fail = 0
function Ok([string]$name) { $script:pass++; Write-Host "PASS  $name" }
function Bad([string]$name, [string]$why) { $script:fail++; Write-Host "FAIL  ${name}: $why" }

# ── the fake release ──────────────────────────────────────────────────────────────────────────
$R = Join-Path $work "release"
$src = Join-Path $work "fake"
New-Item -ItemType Directory -Force -Path $src | Out-Null
Set-Content -LiteralPath (Join-Path $src "go.mod") -Value "module fakeautodoc`n`ngo 1.21`n"
Set-Content -LiteralPath (Join-Path $src "main.go") -Value @'
package main

import (
	"fmt"
	"os"
)

var version = "v0.0.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("autodoc " + version)
	}
}
'@

function Sha([string]$f) { (Get-FileHash -Algorithm SHA256 -LiteralPath $f).Hash.ToLower() }

# Publish adds release Tag: the fake binary zipped, install-autodoc.ps1 and update.ps1, each with its
# .sha256, and "latest" naming it.
function Publish([string]$Tag) {
	$d = Join-Path $R "download\$Tag"
	New-Item -ItemType Directory -Force -Path $d | Out-Null
	$stage = Join-Path $work "build\$Tag"
	New-Item -ItemType Directory -Force -Path $stage | Out-Null
	$exe = Join-Path $stage "autodoc-$Tag-windows-amd64.exe"
	Push-Location $src
	try { & go build -ldflags "-X main.version=$Tag" -o $exe . ; if ($LASTEXITCODE -ne 0) { throw "go build failed" } } finally { Pop-Location }
	$zip = Join-Path $d "autodoc-$Tag-windows-amd64.zip"
	Compress-Archive -LiteralPath $exe -DestinationPath $zip -Force
	Copy-Item -LiteralPath (Join-Path $scripts "install-autodoc.ps1") -Destination $d
	Copy-Item -LiteralPath (Join-Path $scripts "update.ps1") -Destination $d
	foreach ($f in @($zip, (Join-Path $d "install-autodoc.ps1"), (Join-Path $d "update.ps1"))) {
		Set-Content -LiteralPath "$f.sha256" -Value ("{0}  {1}" -f (Sha $f), (Split-Path -Leaf $f))
	}
	Set-Content -LiteralPath (Join-Path $R "latest") -Value $Tag
}

$base = ([Uri](Resolve-Path $work).Path).AbsoluteUri + "/release"
$ps = (Get-Process -Id $PID).Path

# Run runs a script with LOCALAPPDATA and APPDATA under home, and answers its exit code.
function Run([string]$home_, [string]$file, [string[]]$argv) {
	$env:LOCALAPPDATA = Join-Path $home_ "Local"
	$env:APPDATA = Join-Path $home_ "Roaming"
	New-Item -ItemType Directory -Force -Path $env:LOCALAPPDATA, $env:APPDATA | Out-Null
	& $ps -NoProfile -ExecutionPolicy Bypass -File $file @argv *> (Join-Path $home_ "out.txt")
	return $LASTEXITCODE
}

$origLocal, $origRoaming = $env:LOCALAPPDATA, $env:APPDATA
try {
	Publish "v1.0.0"
	$installer = Join-Path $R "download\v1.0.0\install-autodoc.ps1"

	# a fresh install: the binary, the record, update.ps1 and the shortcut
	$h = Join-Path $work "home1"
	$code = Run $h $installer @("-BaseUrl", $base, "-Yes")
	$bin = Join-Path $h "Local\Programs\AutoDoc\autodoc.exe"
	$rec = Join-Path $h "Local\autodoc\install.toml"
	if ($code -ne 0) { Bad "a fresh install" "exit $code: $(Get-Content -Raw (Join-Path $h 'out.txt'))" }
	elseif (-not (Test-Path $bin)) { Bad "a fresh install" "no $bin" }
	elseif ((& $bin --version) -ne "autodoc v1.0.0") { Bad "a fresh install" "the binary says $(& $bin --version)" }
	elseif (-not (Test-Path $rec)) { Bad "a fresh install" "no install record" }
	elseif (-not ((Get-Content -Raw $rec) -match 'version\s*=\s*"v1\.0\.0"' -and (Get-Content -Raw $rec) -match 'method\s*=\s*"script"')) { Bad "a fresh install" "the record is: $(Get-Content -Raw $rec)" }
	elseif (-not (Test-Path (Join-Path $h "Local\autodoc\update.ps1"))) { Bad "a fresh install" "no update.ps1 beside the record" }
	elseif (-not (Test-Path (Join-Path $h "Roaming\Microsoft\Windows\Start Menu\Programs\AutoDoc.lnk"))) { Bad "a fresh install" "no Start menu shortcut" }
	else { Ok "a fresh install: the binary, the record, update.ps1 and the shortcut" }

	# the record's paths are TOML strings: a backslash is escaped, so a TOML reader gets the path back
	if ((Get-Content -Raw $rec) -match ('bin\s*=\s*"' + [regex]::Escape($bin.Replace("\", "\\")) + '"')) { Ok "the record's bin path is a TOML string" }
	else { Bad "the record's bin path is a TOML string" (Get-Content -Raw $rec) }

	# run again: nothing to do
	$code = Run $h $installer @("-BaseUrl", $base, "-Yes")
	if ($code -eq 0 -and (Get-Content -Raw (Join-Path $h 'out.txt')) -match "installed already") { Ok "run again, it says AutoDoc is installed already" }
	else { Bad "run again" "exit $code: $(Get-Content -Raw (Join-Path $h 'out.txt'))" }

	# a checksum that doesn't match installs nothing
	$h2 = Join-Path $work "home2"
	$zipsum = Join-Path $R "download\v1.0.0\autodoc-v1.0.0-windows-amd64.zip.sha256"
	$good = Get-Content -Raw $zipsum
	Set-Content -LiteralPath $zipsum -Value ("0" * 64 + "  autodoc-v1.0.0-windows-amd64.zip")
	$code = Run $h2 $installer @("-BaseUrl", $base, "-Yes")
	Set-Content -LiteralPath $zipsum -Value $good.Trim()
	if ($code -eq 4 -and -not (Test-Path (Join-Path $h2 "Local\Programs\AutoDoc\autodoc.exe"))) { Ok "a checksum that doesn't match installs nothing (exit 4)" }
	else { Bad "a checksum that doesn't match" "exit $code, binary there: $(Test-Path (Join-Path $h2 'Local\Programs\AutoDoc\autodoc.exe'))" }

	# update.ps1 moves to a newer release, and then says it's current
	Publish "v1.1.0"
	$upd = Join-Path $h "Local\autodoc\update.ps1"
	$code = Run $h $upd @("-BaseUrl", $base, "-Yes")
	if ($code -eq 0 -and (& $bin --version) -eq "autodoc v1.1.0" -and (Get-Content -Raw $rec) -match 'version\s*=\s*"v1\.1\.0"') { Ok "update.ps1 installs the newer release" }
	else { Bad "update.ps1 installs the newer release" "exit $code, binary $(& $bin --version): $(Get-Content -Raw (Join-Path $h 'out.txt'))" }
	$code = Run $h $upd @("-BaseUrl", $base, "-Yes")
	if ($code -eq 0 -and (Get-Content -Raw (Join-Path $h 'out.txt')) -match "up to date") { Ok "update.ps1 run again says AutoDoc is up to date" }
	else { Bad "update.ps1 run again" "exit $code: $(Get-Content -Raw (Join-Path $h 'out.txt'))" }

	# update.ps1 refuses an AutoDoc it didn't install
	$h3 = Join-Path $work "home3"
	$code = Run $h3 $upd @("-BaseUrl", $base, "-Yes")
	if ($code -eq 2) { Ok "update.ps1 refuses an AutoDoc with no install record (exit 2)" }
	else { Bad "update.ps1 with no record" "exit $code" }

	# -NoLauncher adds no shortcut
	$h4 = Join-Path $work "home4"
	$code = Run $h4 $installer @("-BaseUrl", $base, "-Yes", "-NoLauncher")
	if ($code -eq 0 -and -not (Test-Path (Join-Path $h4 "Roaming\Microsoft\Windows\Start Menu\Programs\AutoDoc.lnk"))) { Ok "-NoLauncher adds no shortcut" }
	else { Bad "-NoLauncher" "exit $code" }
} finally {
	$env:LOCALAPPDATA, $env:APPDATA = $origLocal, $origRoaming
	Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
Write-Host "installer cells: PASS=$script:pass FAIL=$script:fail"
exit $script:fail
