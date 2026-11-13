# install-autodoc.ps1: installs AutoDoc for this user on Windows, with no administrator rights
# (docs/decisions/install.md#windows).
#
#   irm https://github.com/yongjohnlee80/autodoc/releases/latest/download/install-autodoc.ps1 | iex
#   powershell -ExecutionPolicy Bypass -File install-autodoc.ps1 [-Version vX.Y.Z] [-Yes] [-NoLauncher] [-AddToPath] [-Prefix DIR] [-BaseUrl URL]
#
# It finds the newest release (or the one -Version names), downloads its Windows build, checks it
# against the release's sha256, and installs it as PREFIX\autodoc.exe (PREFIX defaults to
# %LOCALAPPDATA%\Programs\AutoDoc). It adds a Start menu shortcut, writes the install record AutoDoc
# reads (STATE\install.toml, STATE = %LOCALAPPDATA%\autodoc) and installs update.ps1 beside it. Run
# again, it repairs or upgrades: nothing is duplicated. The build is unsigned: Windows may say the
# publisher is unknown the first time it runs.
#
# Exit codes, as install-autodoc.sh's: 0 installed or current; 1 usage; 2 not available on this
# system; 3 declined; 4 a download, a checksum or the install failed (what was installed before is
# untouched).
#
# It never asks for administrator rights. It changes the user's PATH only with -AddToPath; otherwise
# it prints the command that would.

[CmdletBinding()]
param(
	[string]$Version = "",
	[switch]$Yes,
	[switch]$NoLauncher,
	[switch]$AddToPath,
	[string]$Prefix = "",
	[string]$BaseUrl = ""
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue" # Invoke-WebRequest's progress bar slows a download many times over

$RepoReleases = "https://github.com/yongjohnlee80/autodoc/releases"
if ($BaseUrl -eq "") { $BaseUrl = if ($env:AUTODOC_RELEASES) { $env:AUTODOC_RELEASES } else { $RepoReleases } }
$BaseUrl = $BaseUrl.TrimEnd("/")
if ($Prefix -eq "") { $Prefix = Join-Path $env:LOCALAPPDATA "Programs\AutoDoc" }
$Bin = Join-Path $Prefix "autodoc.exe"
$StateDir = Join-Path $env:LOCALAPPDATA "autodoc"
$Record = Join-Path $StateDir "install.toml"

function Say([string]$m) { Write-Host $m }
function Die([int]$code, [string]$m) { [Console]::Error.WriteLine("install-autodoc: $m"); exit $code }

# Fetch downloads Url into File; a file:// URL works too, as the tests use.
function Fetch([string]$Url, [string]$File) {
	if ($Url.StartsWith("file://")) {
		Copy-Item -LiteralPath ([Uri]$Url).LocalPath -Destination $File -Force
		return
	}
	Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $File
}

# Latest is the newest release's tag: GitHub's releases/latest redirect, or a "latest" file at a base
# URL that isn't GitHub's (a mirror, or the tests' fake release).
function Latest {
	if ($BaseUrl.StartsWith("https://github.com/")) {
		$r = Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/latest" -Method Head -MaximumRedirection 5
		$final = $r.BaseResponse.ResponseUri
		if ($null -eq $final) { $final = $r.BaseResponse.RequestMessage.RequestUri } # PowerShell 7
		return ($final.AbsoluteUri -split "/")[-1]
	}
	$f = Join-Path $Tmp "latest"
	Fetch "$BaseUrl/latest" $f
	return (Get-Content -Raw -LiteralPath $f).Trim()
}

# Verified downloads the release's Asset and Asset.sha256 into $Tmp and checks one against the other.
# A failed download is 1, a checksum that doesn't match is 2, a match is 0. Nothing outside $Tmp is
# touched either way.
function Verified([string]$Asset) {
	try {
		Fetch "$BaseUrl/download/$script:Want/$Asset" (Join-Path $Tmp $Asset)
		Fetch "$BaseUrl/download/$script:Want/$Asset.sha256" (Join-Path $Tmp "$Asset.sha256")
	} catch { return 1 }
	$want = ((Get-Content -Raw -LiteralPath (Join-Path $Tmp "$Asset.sha256")) -split "\s+")[0].ToLower()
	$got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $Tmp $Asset)).Hash.ToLower()
	if ($want -ne "" -and $want -eq $got) { return 0 }
	return 2
}

# VersionOf is the vX.Y.Z the binary's --version names, "" when it names none.
function VersionOf([string]$Exe) {
	if (-not (Test-Path -LiteralPath $Exe)) { return "" }
	try { $out = & $Exe --version 2>$null | Out-String } catch { return "" }
	if ($out -match "v\d+\.\d+\.\d+[^\s]*") { return $Matches[0] }
	return ""
}

# RecordValue is a key's value from the install record, "" when there's none.
function RecordValue([string]$Key) {
	if (-not (Test-Path -LiteralPath $Record)) { return "" }
	# the record is UTF-8 (WriteRecord, update.ps1's installer): Windows PowerShell 5.1 would read a
	# BOM-less file in the ANSI code page, and a non-ASCII path would come back garbled
	foreach ($line in Get-Content -LiteralPath $Record -Encoding UTF8) {
		if ($line -match "^$Key\s*=\s*`"(.*)`"\s*$") { return $Matches[1].Replace("\\", "\") }
	}
	return ""
}

# TomlString is s as a TOML basic string's contents: a backslash doubled, a quote escaped.
function TomlString([string]$s) { return $s.Replace("\", "\\").Replace('"', '\"') }

# WriteRecord writes STATE\install.toml, the one place AutoDoc learns the script installed it: the
# same keys install-autodoc.sh writes.
function WriteRecord([string]$Launcher) {
	$now = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
	$since = RecordValue "installed_at"
	if ($since -eq "") { $since = $now }
	New-Item -ItemType Directory -Force -Path $StateDir | Out-Null
	$body = @"
# Written by install-autodoc.ps1 and update.ps1; AutoDoc reads it (its About dialog's Update).
method       = "script"
version      = "$(TomlString $script:Want)"
channel      = "stable"
bin          = "$(TomlString $Bin)"
launcher     = "$(TomlString $Launcher)"
update       = "$(TomlString (Join-Path $StateDir 'update.ps1'))"
installed_at = "$since"
updated_at   = "$now"
"@
	$new = Join-Path $StateDir ".install.toml.new.$PID"
	[IO.File]::WriteAllText($new, $body + "`n", (New-Object Text.UTF8Encoding $false))
	Move-Item -LiteralPath $new -Destination $Record -Force
}

# Shortcut adds a Start menu shortcut to the binary. One that runs this binary is kept as it is; one
# that runs another is replaced only when asked to (-Yes says no, as every question here does).
function Shortcut {
	$dir = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
	$lnk = Join-Path $dir "AutoDoc.lnk"
	$shell = New-Object -ComObject WScript.Shell
	if (Test-Path -LiteralPath $lnk) {
		$old = $shell.CreateShortcut($lnk)
		if ($old.TargetPath -eq $Bin) {
			# ours already: bring one from an older install to the minimized start, nothing else changed
			if ($old.WindowStyle -ne 7) { $old.WindowStyle = 7; $old.Save() }
			return $lnk
		}
		if ($Yes -or -not [Environment]::UserInteractive) { Say "Kept the Start menu shortcut, which runs $($old.TargetPath)."; return $lnk }
		$a = Read-Host "The Start menu shortcut runs $($old.TargetPath). Point it at $Bin? [y/N]"
		if ($a -notmatch "^(y|yes)$") { return $lnk }
	}
	New-Item -ItemType Directory -Force -Path $dir | Out-Null
	$s = $shell.CreateShortcut($lnk)
	$s.TargetPath = $Bin
	$s.Arguments = "--gui"
	$s.WindowStyle = 7 # minimized: AutoDoc gives up the console this opens, and nothing flashes
	$s.WorkingDirectory = $env:USERPROFILE
	$s.IconLocation = "$Bin,0"
	$s.Description = "AutoDoc"
	$s.Save()
	Say "Added the Start menu shortcut AutoDoc."
	return $lnk
}

# --- main ---
$arch = switch ($env:PROCESSOR_ARCHITECTURE) { "AMD64" { "amd64" } "ARM64" { "arm64" } default { $env:PROCESSOR_ARCHITECTURE.ToLower() } }
if ($arch -ne "amd64") { Die 2 "AutoDoc has no Windows build for $arch yet: windows-amd64 is the first." }

$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("autodoc-install-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
try {
	$script:Want = $Version
	if ($script:Want -eq "") {
		try { $script:Want = Latest } catch { Die 4 "couldn't reach $BaseUrl to find the newest AutoDoc" }
	}
	if (-not $script:Want.StartsWith("v")) { $script:Want = "v$script:Want" }

	$installed = VersionOf $Bin
	if ($installed -eq $script:Want) {
		Say "AutoDoc $script:Want is installed already ($Bin)."
	} else {
		$asset = "autodoc-$script:Want-windows-$arch.zip"
		switch (Verified $asset) {
			1 { Die 4 "$asset didn't download (is $script:Want a release with a Windows build?): nothing was installed" }
			2 { Die 4 "$asset didn't match its checksum: nothing was installed" }
		}
		$unpack = Join-Path $Tmp "unpack"
		Expand-Archive -LiteralPath (Join-Path $Tmp $asset) -DestinationPath $unpack -Force
		$new = Join-Path $unpack "autodoc-$script:Want-windows-$arch.exe"
		if (-not (Test-Path -LiteralPath $new)) { Die 4 "$asset holds no autodoc-$script:Want-windows-$arch.exe: nothing was installed" }
		$got = VersionOf $new
		if ($got -ne $script:Want) { Die 4 "the downloaded AutoDoc says `"$got`", not ${script:Want}: nothing was installed" }
		New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
		# A running autodoc.exe can't be replaced, but it can be renamed: the old binary moves aside
		# first, and the new one takes its name in one move. An interrupted install leaves one whole.
		$staged = Join-Path $Prefix ".autodoc.new.$PID.exe"
		Copy-Item -LiteralPath $new -Destination $staged -Force
		$aside = Join-Path $Prefix ".autodoc.old.$PID.exe"
		try {
			if (Test-Path -LiteralPath $Bin) { Move-Item -LiteralPath $Bin -Destination $aside -Force }
			Move-Item -LiteralPath $staged -Destination $Bin -Force
		} catch {
			if ((-not (Test-Path -LiteralPath $Bin)) -and (Test-Path -LiteralPath $aside)) { Move-Item -LiteralPath $aside -Destination $Bin -Force }
			Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
			Die 4 "couldn't install ${Bin}: $_"
		}
		# the old binary, still running perhaps, is removed when it can be: now, or by the next install
		Get-ChildItem -LiteralPath $Prefix -Filter ".autodoc.old.*.exe" -Force | Remove-Item -Force -ErrorAction SilentlyContinue
		Say "Installed AutoDoc $script:Want as $Bin."
	}

	$launcher = RecordValue "launcher"
	if (-not $NoLauncher) { $launcher = Shortcut }

	# update.ps1 from the release, verified, beside the record; a failure keeps the one there
	if ((Verified "update.ps1") -eq 0) {
		New-Item -ItemType Directory -Force -Path $StateDir | Out-Null
		Copy-Item -LiteralPath (Join-Path $Tmp "update.ps1") -Destination (Join-Path $StateDir "update.ps1") -Force
	} else {
		Say "update.ps1 didn't download: the one in $StateDir, if any, is kept."
	}
	WriteRecord $launcher

	$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
	if (-not (($userPath -split ";") -contains $Prefix)) {
		if ($AddToPath) {
			[Environment]::SetEnvironmentVariable("Path", (($userPath.TrimEnd(";"), $Prefix) -join ";").TrimStart(";"), "User")
			Say "Added $Prefix to your PATH: a new terminal finds autodoc."
		} else {
			Say ""
			Say "$Prefix isn't on your PATH. To run autodoc from a terminal, run this once (or install again with -AddToPath):"
			Say "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$Prefix', 'User')"
		}
	}
	if ($installed -ne "" -and $installed -ne $script:Want) { Say "AutoDoc $installed may still be running: restart it to use $script:Want." }
	Say "Done. Update later from AutoDoc's About dialog, or run: powershell -ExecutionPolicy Bypass -File `"$(Join-Path $StateDir 'update.ps1')`""
	exit 0
} finally {
	Remove-Item -LiteralPath $Tmp -Recurse -Force -ErrorAction SilentlyContinue
}
