# Windows Monitor Service

Service Windows léger pour surveiller les événements de démarrage/arrêt et veille/réveil des machines, avec enregistrement dans une base PostgreSQL.

## Fonctionnalités

- ✅ Service Windows natif (démarre avant la connexion utilisateur)
- ✅ Détection des événements système (démarrage, arrêt, veille, réveil)
- ✅ Enregistrement dans PostgreSQL
- ✅ Stockage local temporaire si connexion PostgreSQL non disponible
- ✅ Configuration par fichier JSON
- ✅ Installation/désinstallation automatisée
- ✅ Logs système Windows

## Prérequis

### Pour la compilation
- Go 1.21+
- Windows 10/11 ou Windows Server
- PowerShell 5.1+

### Pour l'exécution
- Windows 10/11 ou Windows Server
- PostgreSQL 12+ accessible
- Privilèges administrateur pour l'installation

## Installation rapide

1. **Compiler le projet**
   ```powershell
   .\build.ps1 -Release
   ```

2. **Configurer PostgreSQL**
   - Exécuter `schema.sql` dans votre base PostgreSQL
   - Modifier `build\config.json` avec vos paramètres

3. **Installer le service**
   ```powershell
   cd build
   .\install.ps1 -Hostname "idhost"
   ```

## Configuration

Le fichier `config.json` doit être placé dans le même répertoire que l'exécutable :

```json
{
  "host": "localhost",
  "port": 5432,
  "user": "monitor_user",
  "password": "your_password_here",
  "database": "monitoring",
  "hostname": "sample" // 6 caractères max
}
```

### Paramètres

| Paramètre | Description | Exemple |
|-----------|-------------|---------|
| `host` | Adresse du serveur PostgreSQL | `"192.168.1.100"` |
| `port` | Port PostgreSQL | `5432` |
| `user` | Utilisateur PostgreSQL | `"monitor_user"` |
| `password` | Mot de passe | `"SecurePass123"` |
| `database` | Nom de la base | `"monitoring"` |
| `hostname` | Nom de cette machine. 6 caractères max | `"dk-spl"` |

## Structure de la base

```sql
CREATE TABLE public.events (
    id SERIAL PRIMARY KEY,
    at TIMESTAMP WITH TIME ZONE NOT NULL,
    host CHAR(6) NOT NULL,
    evt CHAR(3) NOT NULL CHECK (evt IN ('on', 'off'))
);
```

### Types d'événements

| Événement | Type | Description |
|-----------|------|-------------|
| Démarrage système | `on` | La machine démarre |
| Sortie de veille | `on` | Réveil depuis veille/hibernation |
| Arrêt système | `off` | Extinction de la machine |
| Mise en veille | `off` | Veille ou hibernation |

## Utilisation

### Commandes du service

```powershell
# Installation
windows-monitor.exe install

# Démarrage
windows-monitor.exe start

# Arrêt  
windows-monitor.exe stop

# Suppression
windows-monitor.exe remove

# Mode debug (console)
windows-monitor.exe debug
```

### Via PowerShell

```powershell
# Installation complète
.\install.ps1 -Hostname "idhost"

# Désinstallation
.\install.ps1 -Uninstall

# Installation personnalisée
.\install.ps1 -InstallPath "D:\Services\WindowsMonitor"
```

### Vérification du statut

```powershell
# Statut du service
Get-Service WindowsMonitorService

# Logs du service (Event Viewer)
Get-WinEvent -LogName Application | Where-Object {$_.ProviderName -eq "WindowsMonitorService"}

# Logs en temps réel
Get-WinEvent -LogName Application -MaxEvents 10 | Where-Object {$_.ProviderName -eq "WindowsMonitorService"}
```

## Développement

### Compilation

```powershell
# Debug
.\build.ps1

# Release (optimisé)
.\build.ps1 -Release

# Sortie personnalisée
.\build.ps1 -OutputDir "C:\MyBuild"
```

### Dépendances Go

- `github.com/lib/pq` - Driver PostgreSQL
- `golang.org/x/sys/wiandows` - APIs Windows (services)

## Surveillance et maintenance

### Monitoring de la santé

Le service enregistre ses événements dans le journal Windows :
- **Info** : Démarrage/arrêt du service
- **Warning** : Problèmes de connectivité
- **Error** : Erreurs critiques

### Dépannage

| Problème | Solution |
|----------|----------|
| Service ne démarre pas | Vérifier les privilèges et la configuration |
| Pas de connexion PostgreSQL | Vérifier réseau et credentials dans config.json |
| Événements manqués | Vérifier les logs Windows Event Viewer |
| Performance dégradée | Vérifier la taille du fichier events.txt |

### Requêtes utiles

```sql
-- Événements récents
SELECT * FROM public.events 
WHERE at > NOW() - INTERVAL '24 hours' 
ORDER BY at DESC;

-- Temps d'utilisation par machine
SELECT 
    host,
    COUNT(*) as total_events,
    COUNT(CASE WHEN evt = 'on' THEN 1 END) as starts,
    COUNT(CASE WHEN evt = 'off' THEN 1 END) as stops
FROM public.events 
WHERE at > NOW() - INTERVAL '7 days'
GROUP BY host;

-- Durée moyenne d'utilisation
WITH session_times AS (
    SELECT 
        host,
        evt,
        at,
        LEAD(at) OVER (PARTITION BY host ORDER BY at) as next_time
    FROM public.events
    WHERE evt = 'on'
)
SELECT 
    host,
    AVG(EXTRACT(EPOCH FROM (next_time - at))/3600) as avg_hours_per_session
FROM session_times 
WHERE next_time IS NOT NULL
GROUP BY host;
```

## Sécurité

### Bonnes pratiques

1. **Utilisateur PostgreSQL dédié** avec permissions minimales
2. **Mot de passe fort** dans config.json
3. **Chiffrement des connexions** PostgreSQL (SSL)
4. **Restriction réseau** sur les ports PostgreSQL
5. **Monitoring des accès** à la base

### Permissions PostgreSQL

```sql
-- Créer un utilisateur dédié
CREATE USER monitor_user WITH PASSWORD 'SecurePassword123!';

-- Permissions minimales
GRANT CONNECT ON DATABASE monitoring TO monitor_user;
GRANT USAGE ON SCHEMA public TO monitor_user;
GRANT INSERT, SELECT, DELETE ON public.events TO monitor_user;
GRANT USAGE, SELECT ON SEQUENCE public.events_id_seq TO monitor_user;
```

## Licence

Ce projet est sous licence MIT.
