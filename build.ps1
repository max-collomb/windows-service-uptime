# Script de compilation du Windows Monitor
# Compile le service Go pour Windows

param(
    [Parameter(Mandatory=$false)]
    [switch]$Release = $false,
    
    [Parameter(Mandatory=$false)]
    [string]$OutputDir = ".\build"
)

Write-Host "=== Compilation du Windows Monitor ===" -ForegroundColor Green

# Vérifier que Go est installé
try {
    $goVersion = & go version
    Write-Host "Version Go détectée: $goVersion" -ForegroundColor Cyan
} catch {
    Write-Error "Go n'est pas installé ou n'est pas dans le PATH"
    exit 1
}

# Créer le répertoire de sortie
if (-not (Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
}

# Variables d'environnement pour la compilation Windows
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "1"

# Flags de compilation
$buildFlags = @("-ldflags", "-s -w")
if ($Release) {
    Write-Host "Mode Release activé - optimisations supplémentaires" -ForegroundColor Yellow
    $buildFlags += @("-trimpath", "-buildmode=exe")
}

# Nom de l'exécutable
$executableName = "windows-monitor.exe"
$outputPath = Join-Path $OutputDir $executableName

Write-Host "Compilation en cours..." -ForegroundColor Yellow

# Télécharger les dépendances
Write-Host "Téléchargement des dépendances..."
& go mod download
if ($LASTEXITCODE -ne 0) {
    Write-Error "Erreur lors du téléchargement des dépendances"
    exit 1
}

# Compiler
& go build @buildFlags -o $outputPath .
if ($LASTEXITCODE -ne 0) {
    Write-Error "Erreur lors de la compilation"
    exit 1
}

# Copier les fichiers de configuration
$configSource = "config.json"
if (Test-Path $configSource) {
    Copy-Item -Path $configSource -Destination $OutputDir -Force
    Write-Host "Fichier config.json copié" -ForegroundColor Green
}

# Copier le script d'installation
$installScript = "install.ps1"
if (Test-Path $installScript) {
    Copy-Item -Path $installScript -Destination $OutputDir -Force
    Write-Host "Script install.ps1 copié" -ForegroundColor Green
}

# Afficher les informations du fichier compilé
$fileInfo = Get-Item $outputPath
$fileSizeMB = [math]::Round($fileInfo.Length / 1MB, 2)

Write-Host "`n=== Compilation terminée ===" -ForegroundColor Green
Write-Host "Exécutable: $outputPath" -ForegroundColor Cyan
Write-Host "Taille: $fileSizeMB MB" -ForegroundColor Cyan
Write-Host "Répertoire de build: $OutputDir" -ForegroundColor Cyan

Write-Host "`nFichiers prêts pour l'installation:" -ForegroundColor Yellow
Get-ChildItem -Path $OutputDir | ForEach-Object {
    Write-Host "  - $($_.Name)" -ForegroundColor White
}

Write-Host "`nPour installer le service:" -ForegroundColor Yellow
Write-Host "1. Copiez le contenu de '$OutputDir' sur la machine cible" -ForegroundColor White
Write-Host "2. Exécutez 'install.ps1' en tant qu'administrateur" -ForegroundColor White
Write-Host "3. Configurez les paramètres PostgreSQL dans config.json" -ForegroundColor White