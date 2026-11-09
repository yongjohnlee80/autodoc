# update.ps1: updates an AutoDoc that install-autodoc.ps1 installed, on Windows
# (docs/decisions/install.md#windows).
#
#   powershell -ExecutionPolicy Bypass -File %LOCALAPPDATA%\autodoc\update.ps1 [-Yes] [-BaseUrl URL]
#
# AutoDoc's About dialog runs it in the terminal panel. It reads the install record beside it, and
# refuses an AutoDoc another tool installed (update that one its own way). When a newer release
# exists, it runs that release's install-autodoc.ps1, fetched and checksum-verified into a temp dir,
# which installs the new update.ps1 here as its last step: one install path, as on Linux and macOS.
#
# Exit codes, as update.sh's: 0 updated or current; 2 not an AutoDoc install-autodoc.ps1 made; 3 a
# download or a checksum failed (nothing changed); 4 the install failed (the old AutoDoc is
# untouched).

[CmdletBinding()]
param(
	[switch]$Yes,
	[string]$BaseUrl = ""
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$RepoReleases = "https://github.com/yongjohnlee80/autodoc/releases"
if ($BaseUrl -eq "") { $BaseUrl = if ($env:AUTODOC_RELEASES) { $env:AUTODOC_RELEASES } else { $RepoReleases } }
$BaseUrl = $BaseUrl.TrimEnd("/")
$StateDir = Join-Path $env:LOCALAPPDATA "autodoc"
$Record = Join-Path $StateDir "install.toml"

function Say([string]$m) { Write-Host $m }
function Die([int]$code, [string]$m) { [Console]::Error.WriteLine("update: $m"); exit $code }

function RecordValue([string]$Key) {
	if (-not (Test-Path -LiteralPath $Record)) { return "" }
	foreach ($line in Get-Content -LiteralPath $Record) {
		if ($line -match "^$Key\s*=\s*`"(.*)`"\s*$") { return $Matches[1].Replace("\\", "\") }
	}
	return ""
}

function Fetch([string]$Url, [string]$File) {
	if ($Url.StartsWith("file://")) {
		Copy-Item -LiteralPath ([Uri]$Url).LocalPath -Destination $File -Force
		return
	}
	Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $File
}

if (-not (Test-Path -LiteralPath $Record)) { Die 2 "no install record at ${Record}: this AutoDoc wasn't installed by install-autodoc.ps1, so update it the way it was installed" }
if ((RecordValue "method") -ne "script") { Die 2 "$Record doesn't say install-autodoc.ps1 installed AutoDoc: update it the way it was installed" }
$current = RecordValue "version"
$bin = RecordValue "bin"

$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("autodoc-update-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
try {
	try {
		if ($BaseUrl.StartsWith("https://github.com/")) {
			$r = Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/latest" -Method Head -MaximumRedirection 5
			$final = $r.BaseResponse.ResponseUri
			if ($null -eq $final) { $final = $r.BaseResponse.RequestMessage.RequestUri }
			$latest = ($final.AbsoluteUri -split "/")[-1]
		} else {
			Fetch "$BaseUrl/latest" (Join-Path $Tmp "latest")
			$latest = (Get-Content -Raw -LiteralPath (Join-Path $Tmp "latest")).Trim()
		}
	} catch { Die 3 "couldn't reach $BaseUrl to find the newest AutoDoc" }
	if ($latest -eq "") { Die 3 "couldn't tell the newest AutoDoc from $BaseUrl" }
	if ($current -eq $latest) { Say "AutoDoc is up to date ($current)."; exit 0 }

	# the newest installer runs, verified: the release's install-autodoc.ps1 and its .sha256
	$installer = Join-Path $Tmp "install-autodoc.ps1"
	try {
		Fetch "$BaseUrl/download/$latest/install-autodoc.ps1" $installer
		Fetch "$BaseUrl/download/$latest/install-autodoc.ps1.sha256" "$installer.sha256"
	} catch { Die 3 "install-autodoc.ps1 didn't download: nothing changed" }
	$want = ((Get-Content -Raw -LiteralPath "$installer.sha256") -split "\s+")[0].ToLower()
	$got = (Get-FileHash -Algorithm SHA256 -LiteralPath $installer).Hash.ToLower()
	if ($want -eq "" -or $want -ne $got) { Die 3 "install-autodoc.ps1 doesn't match its checksum: nothing changed" }

	Say "Updating AutoDoc $current to $latest…"
	$prefix = if ($bin -ne "") { Split-Path -Parent $bin } else { "" }
	$argv = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $installer, "-Version", $latest, "-BaseUrl", $BaseUrl)
	if ($prefix -ne "") { $argv += @("-Prefix", $prefix) }
	if ((RecordValue "launcher") -eq "") { $argv += "-NoLauncher" }
	if ($Yes) { $argv += "-Yes" }
	$ps = (Get-Process -Id $PID).Path
	& $ps @argv
	$code = $LASTEXITCODE
	if ($code -ne 0) {
		if ($code -eq 4) { Die 4 "the install failed: AutoDoc $current is untouched" }
		exit $code
	}
	Say "Restart AutoDoc to use $latest."
	exit 0
} finally {
	Remove-Item -LiteralPath $Tmp -Recurse -Force -ErrorAction SilentlyContinue
}
