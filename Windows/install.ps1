# Installiert die Zebra-Bruecke (lokaler IPP-Drucker -> ZPL) und legt den Windows-Drucker an.
# Aufruf: install.cmd [IP-Adresse des Druckers]      Entfernen: deinstallieren.cmd
param([string]$PrinterIP = "", [switch]$Uninstall)
$ErrorActionPreference = "Stop"
$TaskName = "ZebraBruecke"
$PrinterName = "Zebra GK420d"
$Dst = Join-Path $env:ProgramFiles "ZebraBruecke"
$Url = "http://127.0.0.1:631/ipp/print"

$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    $a = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
    if ($PrinterIP) { $a += " -PrinterIP $PrinterIP" }
    if ($Uninstall) { $a += " -Uninstall" }
    Start-Process powershell -Verb RunAs -ArgumentList $a
    exit
}

function Stop-Bridge {
    if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) { Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue }
    Get-Process -Name "zebra-bruecke" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 500
}

try {
    if ($Uninstall) {
        Get-Printer -Name $PrinterName -ErrorAction SilentlyContinue | Remove-Printer
        Stop-Bridge
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
        if (Test-Path $Dst) { Remove-Item $Dst -Recurse -Force }
        Write-Host "Zebra-Bruecke und Drucker wurden entfernt."
    }
    else {
        $exe = "zebra-bruecke-x64.exe"
        if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") { $exe = "zebra-bruecke-arm64.exe" }
        $srcExe = Join-Path $PSScriptRoot $exe
        if (-not (Test-Path $srcExe)) { throw "$exe fehlt neben install.ps1" }

        Stop-Bridge
        New-Item -ItemType Directory -Force -Path $Dst | Out-Null
        Copy-Item $srcExe (Join-Path $Dst "zebra-bruecke.exe") -Force
        Copy-Item (Join-Path $PSScriptRoot "etiketten.txt") $Dst -Force

        # Konfiguration: vorhandene Einstellungen behalten, nur die Adresse setzen
        $cfgPath = Join-Path $Dst "zebra-bruecke.json"
        $old = ""
        if (Test-Path $cfgPath) {
            $cfg = Get-Content $cfgPath -Raw | ConvertFrom-Json
            $old = ($cfg.printer_address -split ":")[0]
        }
        else {
            $cfg = [pscustomobject]@{ printer_address = ""; listen = "127.0.0.1:631"; printer_name = $PrinterName; dpi = 203
                max_print_width_mm = 104; darkness = -1; speed = 0; media_tracking = "gap"; rotate180 = $false; dither = $false; threshold = 50 }
        }
        if (-not $PrinterIP) {
            $q = "IP-Adresse des Zebra-Druckers"
            if ($old) { $q += " [$old]" }
            $PrinterIP = Read-Host $q
            if (-not $PrinterIP) { $PrinterIP = $old }
        }
        if (-not $PrinterIP) { throw "Keine IP-Adresse angegeben." }
        $cfg.printer_address = "${PrinterIP}:9100"
        [IO.File]::WriteAllText($cfgPath, ($cfg | ConvertTo-Json), (New-Object Text.UTF8Encoding $false))

        # Autostart als Hintergrunddienst (geplante Aufgabe beim Systemstart)
        $action = New-ScheduledTaskAction -Execute (Join-Path $Dst "zebra-bruecke.exe") -WorkingDirectory $Dst
        $trigger = New-ScheduledTaskTrigger -AtStartup
        $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
        $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable `
            -ExecutionTimeLimit (New-TimeSpan -Seconds 0) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
        Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
        Start-ScheduledTask -TaskName $TaskName

        $ok = $false
        foreach ($i in 1..20) {
            Start-Sleep -Milliseconds 500
            try { Invoke-WebRequest "http://127.0.0.1:631/" -UseBasicParsing -TimeoutSec 2 | Out-Null; $ok = $true; break } catch { }
        }
        if (-not $ok) { throw "Die Bruecke antwortet nicht auf Port 631. Details: $Dst\zebra-bruecke.log" }
        Write-Host "Bruecke laeuft. Statusseite und Testdruck: http://127.0.0.1:631/"

        # Drucker neu anlegen, damit Windows die aktuellen Formate einliest
        Get-Printer -Name $PrinterName -ErrorAction SilentlyContinue | Remove-Printer
        try {
            try { Add-Printer -Name $PrinterName -IppURL $Url } catch { Add-Printer -IppURL $Url }
            Write-Host "Drucker `"$PrinterName`" wurde angelegt."
        }
        catch {
            Write-Warning "Der Drucker konnte nicht automatisch angelegt werden: $($_.Exception.Message)"
            Write-Host "Bitte von Hand: Einstellungen > Bluetooth und Geraete > Drucker und Scanner > Geraet hinzufuegen >"
            Write-Host "Manuell hinzufuegen > 'Freigegebenen Drucker ueber den Namen auswaehlen' und diese Adresse eintragen:"
            Write-Host "    $Url"
        }
    }
}
catch {
    Write-Host "FEHLER: $($_.Exception.Message)" -ForegroundColor Red
}
Read-Host "Eingabetaste zum Schliessen"
