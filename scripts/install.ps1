# irm https://raw.githubusercontent.com/ryanzhangtianran/homie/main/scripts/install.ps1 | iex
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$dir = "$HOME\.local\bin"; New-Item -ItemType Directory -Force $dir | Out-Null
Invoke-WebRequest "https://github.com/ryanzhangtianran/homie/releases/latest/download/homie_windows_$arch.exe" -OutFile "$dir\homie.exe"
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {  # rerunning the installer must not add it again
    [Environment]::SetEnvironmentVariable('Path', "$dir;$userPath", 'User')
}
Write-Host "installed $dir\homie.exe (restart your terminal)"
