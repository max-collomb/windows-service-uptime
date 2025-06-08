package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName = "WindowsMonitorService"
	serviceDesc = "Service de monitoring des événements Windows"
)

type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	Hostname string `json:"hostname"`
}

type Event struct {
	ID   int       `json:"id"`
	At   time.Time `json:"at"`
	Host string    `json:"host"`
	Evt  string    `json:"evt"`
}

type WindowsMonitorService struct {
	config        *Config
	logger        *log.Logger
	eventFile     string
	lastEventType string
	retryTimer    *time.Timer
	retryRunning  bool
}

func main() {
	// Déterminer le mode d'exécution
	if len(os.Args) < 2 {
		runService()
		return
	}

	cmd := os.Args[1]
	switch cmd {
	case "install":
		installService()
	case "remove":
		removeService()
	case "start":
		startService()
	case "stop":
		stopService()
	case "debug":
		runDebug()
	default:
		fmt.Printf("Commandes disponibles: install, remove, start, stop, debug\n")
	}
}

func runService() {
	isWindowsService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("Erreur lors de la vérification de session: %v", err)
	}

	if isWindowsService {
		runWindowsService()
		return
	}

	runDebug()
}

func runWindowsService() {
	elog, err := eventlog.Open(serviceName)
	if err != nil {
		log.Printf("ERREUR: Impossible d'ouvrir le journal des événements: %v", err)
		return
	}
	defer elog.Close()

	// Créer un fichier de log pour le service
	logFile, err := os.OpenFile("C:\\Program Files\\WindowsMonitor\\service.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		elog.Error(1, fmt.Sprintf("Impossible de créer le fichier de log: %v", err))
		return
	}
	defer logFile.Close()

	log.SetOutput(logFile)
	log.Printf("Service démarré")
	elog.Info(1, fmt.Sprintf("Service %s démarré", serviceName))

	service := &WindowsMonitorService{}
	err = svc.Run(serviceName, service)
	if err != nil {
		errMsg := fmt.Sprintf("Service %s erreur: %v", serviceName, err)
		log.Printf("ERREUR: %s", errMsg)
		elog.Error(1, errMsg)
		return
	}

	log.Printf("Service arrêté")
	elog.Info(1, fmt.Sprintf("Service %s arrêté", serviceName))
}

func runDebug() {
	// Configurer la journalisation dans un fichier
	logFile, err := os.OpenFile("debug.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Erreur création fichier debug.log: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	log.SetOutput(logFile)
	log.Printf("Démarrage du mode debug")

	// Charger la configuration pour tester
	config, err := loadConfig()
	if err != nil {
		log.Printf("ERREUR: Impossible de charger la configuration: %v", err)
		fmt.Printf("ERREUR: Impossible de charger la configuration: %v\n", err)
		os.Exit(1)
	}
	log.Printf("Configuration chargée avec succès: %+v", config)

	service := &WindowsMonitorService{}
	err = debug.Run(serviceName, service)
	if err != nil {
		log.Printf("ERREUR: %v", err)
		fmt.Printf("ERREUR: %v\n", err)
		os.Exit(1)
	}
}

func (m *WindowsMonitorService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent
	changes <- svc.Status{State: svc.StartPending}

	// Initialiser le service
	err := m.init()
	if err != nil {
		m.logError(fmt.Sprintf("Erreur initialisation: %v", err))
		return
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	// Enregistrer l'événement de démarrage
	m.recordEvent("on")

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			// Enregistrer l'événement d'arrêt
			m.recordEvent("off")
		case svc.PowerEvent:
			if c.EventType == 0x4 { // PBT_APMSUSPEND
				m.recordEvent("off")
			} else if c.EventType == 0x7 { // PBT_APMRESUMESUSPEND
				m.recordEvent("on")
			}
		default:
			m.logError(fmt.Sprintf("Commande non supportée: %d", c.Cmd))
		}
		if c.Cmd == svc.Stop || c.Cmd == svc.Shutdown {
			break
		}
	}

	changes <- svc.Status{State: svc.StopPending}
	return
}

func (m *WindowsMonitorService) init() error {
	// Créer un fichier de log pour le service
	logFile, err := os.OpenFile("service.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("impossible de créer le fichier de log: %w", err)
	}
	m.logger = log.New(io.MultiWriter(os.Stdout, logFile), "[WindowsMonitor] ", log.LstdFlags)
	m.logger.Printf("Initialisation du service...")

	// Charger la configuration
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("impossible de déterminer le chemin de l'exécutable: %w", err)
	}
	m.logger.Printf("Chemin de l'exécutable: %s", exePath)

	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("impossible de charger la configuration: %w", err)
	}
	m.config = config
	m.logger.Printf("Configuration chargée: %+v", config)

	// Définir le chemin du fichier d'événements
	m.eventFile = filepath.Join(filepath.Dir(exePath), "events.txt")
	m.logger.Printf("Fichier d'événements: %s", m.eventFile)

	// Tester la connexion à la base de données
	err = m.testDatabaseConnection()
	if err != nil {
		return fmt.Errorf("impossible de se connecter à la base de données: %w", err)
	}
	m.logger.Printf("Connexion à la base de données testée avec succès")

	return nil
}

func (m *WindowsMonitorService) testDatabaseConnection() error {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		m.config.Host, m.config.Port, m.config.User, m.config.Password, m.config.Database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("erreur d'ouverture de la connexion: %w", err)
	}
	defer db.Close()

	return db.Ping()
}

func (m *WindowsMonitorService) recordEvent(eventType string) {
	now := time.Now()

	if m.lastEventType == eventType {
		m.logger.Printf("Événement ignoré (doublon): %s", eventType)
		return
	}

	// Tente d'insérer en base de données
	err := m.insertEventDB(now, eventType)
	if err == nil {
		m.lastEventType = eventType
		m.logger.Printf("Événement enregistré en base: %s à %s", eventType, now.Format(time.RFC3339))
		return
	}

	// En cas d'échec, écrit dans le fichier
	m.logger.Printf("Échec d'insertion en base: %v - Sauvegarde locale", err)
	eventLine := fmt.Sprintf("%d %s\n", now.Unix(), eventType)

	if err := m.appendToEventFile(eventLine); err != nil {
		m.logError(fmt.Sprintf("Erreur écriture fichier événements: %v", err))
		return
	}

	// Démarre le timer de reconnexion si ce n'est pas déjà fait
	m.startRetryTimer()

	m.lastEventType = eventType
	m.logger.Printf("Événement enregistré localement: %s à %s", eventType, now.Format(time.RFC3339))
}

func (m *WindowsMonitorService) appendToEventFile(line string) error {
	file, err := os.OpenFile(m.eventFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(line)
	return err
}

func (m *WindowsMonitorService) logError(msg string) {
	if m.logger != nil {
		m.logger.Printf("ERREUR: %s", msg)
	}
}

func loadConfig() (*Config, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(filepath.Dir(exePath), "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func installService() {
	exepath, err := os.Executable()
	if err != nil {
		log.Fatalf("Impossible de déterminer le chemin de l'exécutable: %v", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		log.Fatalf("Cannot connect to service manager: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		log.Printf("Service %s existe déjà", serviceName)
		return
	}

	s, err = m.CreateService(serviceName, exepath, mgr.Config{
		DisplayName: serviceDesc,
		Description: serviceDesc,
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		log.Fatalf("Impossible de créer le service: %v", err)
	}
	defer s.Close()

	err = eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err != nil {
		s.Delete()
		log.Fatalf("Impossible d'installer l'event log: %v", err)
	}

	log.Printf("Service %s installé avec succès", serviceName)
}

func removeService() {
	m, err := mgr.Connect()
	if err != nil {
		log.Fatalf("Cannot connect to service manager: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		log.Fatalf("Service %s n'existe pas: %v", serviceName, err)
	}
	defer s.Close()

	err = s.Delete()
	if err != nil {
		log.Fatalf("Impossible de supprimer le service: %v", err)
	}

	err = eventlog.Remove(serviceName)
	if err != nil {
		log.Fatalf("Impossible de supprimer l'event log: %v", err)
	}

	log.Printf("Service %s supprimé avec succès", serviceName)
}

func startService() {
	m, err := mgr.Connect()
	if err != nil {
		log.Fatalf("Cannot connect to service manager: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		log.Fatalf("Could not access service: %v", err)
	}
	defer s.Close()

	err = s.Start()
	if err != nil {
		log.Fatalf("Could not start service: %v", err)
	}

	log.Printf("Service %s démarré", serviceName)
}

func stopService() {
	m, err := mgr.Connect()
	if err != nil {
		log.Fatalf("Cannot connect to service manager: %v", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		log.Fatalf("Could not access service: %v", err)
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		log.Fatalf("Could not send control=%d: %v", svc.Stop, err)
	}

	timeout := time.Now().Add(10 * time.Second)
	for status.State != svc.Stopped {
		if timeout.Before(time.Now()) {
			log.Fatalf("Timeout waiting for service to go to state=%d", svc.Stopped)
		}
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			log.Fatalf("Could not retrieve service status: %v", err)
		}
	}

	log.Printf("Service %s arrêté", serviceName)
}

func (m *WindowsMonitorService) insertEventDB(timestamp time.Time, eventType string) error {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		m.config.Host, m.config.Port, m.config.User, m.config.Password, m.config.Database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("erreur de connexion à la base: %w", err)
	}
	defer db.Close()

	// Test la connexion
	if err = db.Ping(); err != nil {
		return fmt.Errorf("erreur de ping à la base: %w", err)
	}

	_, err = db.Exec("INSERT INTO public.events (at, host, evt) VALUES ($1, $2, $3)",
		timestamp, m.config.Hostname, eventType)
	if err != nil {
		return fmt.Errorf("erreur d'insertion en base: %w", err)
	}

	return nil
}

func (m *WindowsMonitorService) startRetryTimer() {
	if m.retryRunning {
		return
	}

	m.retryRunning = true
	m.retryTimer = time.NewTimer(time.Minute)

	go func() {
		for m.retryRunning {
			<-m.retryTimer.C

			// Lit et traite les événements du fichier
			if err := m.processStoredEvents(); err != nil {
				m.logger.Printf("Erreur lors du traitement des événements stockés: %v", err)
				m.retryTimer.Reset(time.Minute)
				continue
			}

			// Les événements ont été traités avec succès
			m.retryRunning = false
			m.logger.Printf("Événements traités avec succès")
			return
		}
	}()
}

func (m *WindowsMonitorService) processStoredEvents() error {
	// Lit le fichier d'événements
	data, err := os.ReadFile(m.eventFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Pas d'événements à traiter
		}
		return err
	}

	// Si le fichier est vide, rien à faire
	if len(data) == 0 {
		return nil
	}

	// Parse chaque ligne et insère en base
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var timestamp int64
		var eventType string
		if _, err := fmt.Sscanf(line, "%d %s", &timestamp, &eventType); err != nil {
			m.logger.Printf("Erreur de parsing de la ligne: %s", line)
			continue
		}

		// Insère l'événement en base
		if err := m.insertEventDB(time.Unix(timestamp, 0), eventType); err != nil {
			return err
		}
	}

	// Tous les événements ont été traités, on peut vider le fichier
	return os.WriteFile(m.eventFile, []byte(""), 0644)
}
