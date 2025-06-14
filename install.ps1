# Script d'installation du service Uptime Monitor
# Exécuter en tant qu'administrateur

param(
    [Parameter(Mandatory=$false)]
    [string]$InstallPath = "C:\Program Files\UptimeMonitor",
    
    [Parameter(Mandatory=$false)]
    [switch]$Uninstall = $false,

    [Parameter(Mandatory=$false)]
    [string]$Hostname
)

# Vérifier les privilèges administrateur
if (-NOT ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator"))
{
    Write-Error "Ce script doit être exécuté en tant qu'administrateur"
    exit 1
}

$ServiceName = "UptimeMonitorService"
$ExecutableName = "uptime-monitor.exe"
$ConfigName = "config.json"

function Test-ServiceExists {
    param([string]$ServiceName)
    return $null -ne (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue)
}

function Stop-ServiceIfRunning {
    param([string]$ServiceName)
    $service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($service -and $service.Status -eq "Running") {
        Write-Host "Arrêt du service $ServiceName..."
        Stop-Service -Name $ServiceName -Force
        Start-Sleep -Seconds 3
    }
}

function Get-ScriptDirectory {
    # Plusieurs méthodes pour obtenir le répertoire du script
    if ($PSScriptRoot) {
        return $PSScriptRoot
    } elseif ($MyInvocation.MyCommand.Path) {
        return Split-Path -Parent $MyInvocation.MyCommand.Path
    } else {
        # Fallback sur le répertoire courant
        return Get-Location
    }
}

function Uninstall-UptimeMonitor {
    Write-Host "=== Désinstallation du service Uptime Monitor ===" -ForegroundColor Yellow
    
    # Arrêter le service s'il fonctionne
    Stop-ServiceIfRunning -ServiceName $ServiceName
    
    # Supprimer le service
    if (Test-ServiceExists -ServiceName $ServiceName) {
        Write-Host "Suppression du service..."
        $installExe = Join-Path $InstallPath $ExecutableName
        if (Test-Path $installExe) {
            & $installExe remove
            if ($LASTEXITCODE -eq 0) {
                Write-Host "Service supprimé avec succès" -ForegroundColor Green
            } else {
                Write-Warning "Erreur lors de la suppression du service"
            }
        } else {
            Write-Warning "Exécutable non trouvé pour la suppression du service"
            # Tentative de suppression manuelle du service
            try {
                Remove-Service -Name $ServiceName -ErrorAction Stop
                Write-Host "Service supprimé manuellement avec succès" -ForegroundColor Green
            } catch {
                Write-Warning "Impossible de supprimer le service manuellement"
            }
        }
    }
    
    # Supprimer les fichiers
    if (Test-Path $InstallPath) {
        Write-Host "Suppression des fichiers..."
        Remove-Item -Path $InstallPath -Recurse -Force
        Write-Host "Fichiers supprimés" -ForegroundColor Green
    }
    
    Write-Host "Désinstallation terminée" -ForegroundColor Green
}

function Install-UptimeMonitor {
    Write-Host "=== Installation du service Uptime Monitor ===" -ForegroundColor Green
    
    # Créer le répertoire d'installation
    if (-not (Test-Path $InstallPath)) {
        Write-Host "Création du répertoire $InstallPath..."
        New-Item -ItemType Directory -Path $InstallPath -Force | Out-Null
    }
    
    # Obtenir le répertoire du script
    $currentDir = Get-ScriptDirectory
    Write-Host "Répertoire source: $currentDir" -ForegroundColor Cyan
    
    $sourceBinary = Join-Path $currentDir $ExecutableName
    $sourceConfig = Join-Path $currentDir $ConfigName
    
    # Vérifier que l'exécutable existe
    if (-not (Test-Path $sourceBinary)) {
        Write-Error "Fichier $ExecutableName introuvable dans le répertoire $currentDir"
        Write-Host "Fichiers présents dans le répertoire:" -ForegroundColor Yellow
        Get-ChildItem $currentDir | ForEach-Object { Write-Host "  - $($_.Name)" }
        exit 1
    }
    
    Write-Host "Copie de l'exécutable..."
    Copy-Item -Path $sourceBinary -Destination $InstallPath -Force
    
    # Copier le fichier de configuration
    if (Test-Path $sourceConfig) {
        Write-Host "Copie du fichier de configuration..."
        Copy-Item -Path $sourceConfig -Destination $InstallPath -Force
          # Mise à jour du hostname dans le fichier copié
        $configPath = Join-Path $InstallPath $ConfigName
        $config = Get-Content $configPath -Raw | ConvertFrom-Json
        $config.hostname = $Hostname
        $jsonContent = $config | ConvertTo-Json -Depth 10
        [System.IO.File]::WriteAllText($configPath, $jsonContent, [System.Text.UTF8Encoding]::new($false))
        Write-Host "Hostname mis à jour dans le fichier de configuration" -ForegroundColor Green
    } else {        Write-Warning "Fichier config.json non trouvé. Création d'un fichier par défaut..."
        $defaultConfig = @{
            host = "localhost"
            port = 5432
            user = "monitor_user"  
            password = "your_password_here"
            database = "monitoring"
            hostname = $Hostname
        }
        $jsonContent = $defaultConfig | ConvertTo-Json -Depth 10
        [System.IO.File]::WriteAllText((Join-Path $InstallPath $ConfigName), $jsonContent, [System.Text.UTF8Encoding]::new($false))
    }
    
    # Installer le service
    Write-Host "Installation du service..."
    $installExe = Join-Path $InstallPath $ExecutableName
    & $installExe install
    
    if ($LASTEXITCODE -eq 0) {
        Write-Host "Service installé avec succès" -ForegroundColor Green
        
        # Démarrer le service
        Write-Host "Démarrage du service..."
        & $installExe start
        
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Service démarré avec succès" -ForegroundColor Green
        } else {
            Write-Warning "Erreur lors du démarrage du service"
        }
    } else {
        Write-Error "Erreur lors de l'installation du service"
        exit 1
    }
    
    Write-Host "`n=== Installation terminée ===" -ForegroundColor Green
    Write-Host "Service installé dans: $InstallPath" -ForegroundColor Cyan
    Write-Host "Configuration: $InstallPath\$ConfigName" -ForegroundColor Cyan
    Write-Host "`nCommandes utiles:" -ForegroundColor Yellow
    Write-Host "- Arrêter le service: & '$installExe' stop" -ForegroundColor White
    Write-Host "- Démarrer le service: & '$installExe' start" -ForegroundColor White
    Write-Host "- Mode debug: & '$installExe' debug" -ForegroundColor White
    Write-Host "`nN'oubliez pas de configurer les paramètres PostgreSQL dans config.json !" -ForegroundColor Red
}

# Exécution principale
try {
    if ($Uninstall) {
        Uninstall-UptimeMonitor
    } else {
        # Vérifier si le service existe déjà
        if (Test-ServiceExists -ServiceName $ServiceName) {
            Write-Host "Le service $ServiceName existe déjà." -ForegroundColor Yellow
            $response = Read-Host "Voulez-vous le réinstaller ? (y/N)"
            if ($response -eq "y" -or $response -eq "Y") {
                Uninstall-UptimeMonitor
                Start-Sleep -Seconds 2
                Install-UptimeMonitor
            } else {
                Write-Host "Installation annulée" -ForegroundColor Yellow
            }
        } else {
            Install-UptimeMonitor
        }
    }
} catch {
    Write-Error "Erreur lors de l'exécution: $($_.Exception.Message)"
    exit 1
}

# Pause pour laisser le temps de lire les messages
Write-Host "`nAppuyez sur une touche pour continuer..." -ForegroundColor Gray
$null = $Host.UI.RawUI.ReadKey("NoEcho,IncludeKeyDown")