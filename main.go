package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	_ "github.com/lib/pq"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName = "UptimeMonitorService"
	serviceDesc = "Uptime Monitoring Service"
)

// WTS Session notification constants
const (
	WM_WTSSESSION_CHANGE    = 0x02B1
	WTS_SESSION_LOCK        = 0x7
	WTS_SESSION_UNLOCK      = 0x8
	NOTIFY_FOR_ALL_SESSIONS = 1
)

var (
	wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")

	procWTSRegisterSessionNotification   = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
	procCreateWindowEx                   = user32.NewProc("CreateWindowExW")
	procDefWindowProc                    = user32.NewProc("DefWindowProcW")
	procRegisterClass                    = user32.NewProc("RegisterClassW")
	procGetMessage                       = user32.NewProc("GetMessageW")
	procDispatchMessage                  = user32.NewProc("DispatchMessageW")
	procPostQuitMessage                  = user32.NewProc("PostQuitMessage")
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

type MSG struct {
	Hwnd    windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

type WNDCLASS struct {
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
}

type UptimeMonitorService struct {
	config       *Config
	eventFile    string
	lastEvent    string
	retryTimer   *time.Timer
	retryRunning bool

	// WTS monitoring
	hwnd       windows.Handle
	stopWTS    chan struct{}
	wtsStarted bool
	mu         sync.Mutex
}

func (service *UptimeMonitorService) Execute(args []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (ssec bool, errno uint32) {

	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent

	status <- svc.Status{State: svc.StartPending}

	// Démarrer la surveillance WTS
	service.startWTSMonitoring()

	status <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	// start event
	log.Print("Starting service")
	service.recordEvent("on")

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			service.recordEvent("off")
			log.Print("Shutting down service")
			service.stopWTSMonitoring()
			break loop
		case svc.PowerEvent:
			// Log the received power event type for diagnostics
			log.Printf("PowerEvent received. EventType: 0x%X", c.EventType)
			switch c.EventType {
			case 0x4: // PBT_PBT_APMSUSPEND
				log.Printf("PBT_APMSUSPEND (0x%X) detected. Recording 'off'.", c.EventType)
				service.recordEvent("off")
			case 0x7: // PBT_APMRESUMESUSPEND
				log.Printf("PBT_APMRESUMESUSPEND (0x%X) detected. Recording 'on'.", c.EventType)
				service.recordEvent("on")
			default:
				log.Printf("Unhandled PowerEvent EventType: 0x%X. No action taken.", c.EventType)
			}
		default:
			log.Printf("Unexpected service control request #%d", c)
		}
	}

	status <- svc.Status{State: svc.StopPending}
	return
}

func (service *UptimeMonitorService) startWTSMonitoring() {
	service.mu.Lock()
	defer service.mu.Unlock()

	if service.wtsStarted {
		return
	}

	service.stopWTS = make(chan struct{})

	go func() {
		if err := service.createWTSWindow(); err != nil {
			log.Printf("Failed to create WTS monitoring window: %v", err)
			return
		}

		service.mu.Lock()
		service.wtsStarted = true
		service.mu.Unlock()

		log.Print("WTS session monitoring started")
		service.wtsMessageLoop()
	}()
}

func (service *UptimeMonitorService) stopWTSMonitoring() {
	service.mu.Lock()
	defer service.mu.Unlock()

	if !service.wtsStarted {
		return
	}

	close(service.stopWTS)

	if service.hwnd != 0 {
		procWTSUnRegisterSessionNotification.Call(uintptr(service.hwnd))
		procPostQuitMessage.Call(0)
	}

	service.wtsStarted = false
	log.Print("WTS session monitoring stopped")
}

func (service *UptimeMonitorService) createWTSWindow() error {
	className, _ := windows.UTF16PtrFromString("UptimeMonitorWTSClass")
	windowName, _ := windows.UTF16PtrFromString("UptimeMonitorWTS")

	wndClass := WNDCLASS{
		LpfnWndProc:   windows.NewCallback(service.wtsWndProc),
		LpszClassName: className,
	}

	ret, _, _ := procRegisterClass.Call(uintptr(unsafe.Pointer(&wndClass)))
	if ret == 0 {
		return fmt.Errorf("failed to register WTS window class")
	}

	ret, _, _ = procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0, 0, 0, 0, 0, 0, 0, 0, 0,
	)

	if ret == 0 {
		return fmt.Errorf("failed to create WTS window")
	}

	service.hwnd = windows.Handle(ret)

	// Enregistrer pour les notifications de session
	ret, _, err := procWTSRegisterSessionNotification.Call(
		uintptr(service.hwnd),
		NOTIFY_FOR_ALL_SESSIONS,
	)

	if ret == 0 {
		return fmt.Errorf("failed to register WTS session notification: %v", err)
	}

	return nil
}

func (service *UptimeMonitorService) wtsWndProc(hwnd windows.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_WTSSESSION_CHANGE:
		service.handleWTSSessionChange(wParam, lParam)
	}

	ret, _, _ := procDefWindowProc.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func (service *UptimeMonitorService) handleWTSSessionChange(wParam, lParam uintptr) {
	switch wParam {
	case WTS_SESSION_LOCK:
		log.Print("WTS_SESSION_LOCK detected. Recording 'off'.")
		service.recordEvent("off")
	case WTS_SESSION_UNLOCK:
		log.Print("WTS_SESSION_UNLOCK detected. Recording 'on'.")
		service.recordEvent("on")
	default:
		log.Printf("WTS Session event: 0x%X (ignored)", wParam)
	}
}

func (service *UptimeMonitorService) wtsMessageLoop() {
	var msg MSG

	for {
		select {
		case <-service.stopWTS:
			return
		default:
			ret, _, _ := procGetMessage.Call(
				uintptr(unsafe.Pointer(&msg)),
				uintptr(service.hwnd),
				0, 0,
			)

			if ret == 0 { // WM_QUIT
				return
			}

			if ret == ^uintptr(0) { // -1 = error
				log.Printf("GetMessage error")
				return
			}

			procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}
}

func (service *UptimeMonitorService) recordEvent(event string) {
	log.Printf("Event %s", event)
	now := time.Now()

	if service.lastEvent == event {
		log.Printf("Ignored event: %s", event)
		return
	}

	// INSERT
	err := service.insertEventDB(now, event)
	if err == nil {
		service.lastEvent = event
		log.Printf("Event saved in DB: %s at %s", event, now.Format(time.RFC3339))
		return
	}

	// If insert failed, write to a file
	log.Printf("Failed to insert in DB: %v - saving locally", err)
	eventLine := fmt.Sprintf("%d %s\n", now.Unix(), event)

	if err := service.appendToEventFile(eventLine); err != nil {
		log.Printf("Error writing to file: %v", err)
		return
	}

	// Start timer to retry saving in DB
	service.startRetryTimer()

	service.lastEvent = event
	log.Printf("Event saved locally: %s à %s", event, now.Format(time.RFC3339))
}

func (service *UptimeMonitorService) insertEventDB(timestamp time.Time, event string) error {
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		service.config.Host,
		service.config.Port,
		service.config.User,
		service.config.Password,
		service.config.Database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("error while connecting to DB: %w", err)
	}
	defer db.Close()

	// connection test
	if err = db.Ping(); err != nil {
		return fmt.Errorf("error while pinging DB: %w", err)
	}

	_, err = db.Exec("INSERT INTO public.events (at, host, evt) VALUES ($1, $2, $3)", timestamp, service.config.Hostname, event)
	if err != nil {
		return fmt.Errorf("error while inserting in DB: %w", err)
	}

	return nil
}

func (service *UptimeMonitorService) appendToEventFile(line string) error {
	file, err := os.OpenFile(service.eventFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(line)
	return err
}

func (service *UptimeMonitorService) startRetryTimer() {
	if service.retryRunning {
		return
	}

	service.retryRunning = true
	service.retryTimer = time.NewTimer(time.Minute)

	go func() {
		for service.retryRunning {
			<-service.retryTimer.C

			// Read and send event in the file
			if err := service.processStoredEvents(); err != nil {
				log.Printf("Error while processing stored events: %v", err)
				service.retryTimer.Reset(time.Minute)
				continue
			}

			// Events have been successfully sent
			service.retryRunning = false
			log.Printf("Events sent successfully")
			return
		}
	}()
}

func (service *UptimeMonitorService) processStoredEvents() error {
	// Read events file
	data, err := os.ReadFile(service.eventFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No event to process
		}
		return err
	}

	// File is empty, do nothing
	if len(data) == 0 {
		return nil
	}

	// Parse each line and insert in DB
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var timestamp int64
		var eventType string
		if _, err := fmt.Sscanf(line, "%d %s", &timestamp, &eventType); err != nil {
			log.Printf("Error while parsing line: %s", line)
			continue
		}

		// Insère l'événement en base
		if err := service.insertEventDB(time.Unix(timestamp, 0), eventType); err != nil {
			return err
		}
	}

	// Tous les événements ont été traités, on peut vider le fichier
	return os.WriteFile(service.eventFile, []byte(""), 0644)
}

func loadConfig(configPath string) (*Config, error) {
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

func runService(service *UptimeMonitorService, isDebug bool) {
	if isDebug {
		err := debug.Run("uptimeMonitorService", service)
		if err != nil {
			log.Fatalln("Error running service in debug mode.")
		}
	} else {
		err := svc.Run("uptimeMonitorService", service)
		if err != nil {
			log.Fatalln("Error running service in Service Control mode.")
		}
	}
}

func main() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		// This initial log goes to stderr. If running as a service, it's likely lost.
		// It's a best-effort log before more robust logging is available.
		log.Printf("Warning: Failed to determine if session is interactive: %v. Assuming non-interactive.", err)
		isService = false // Assume interactive (non service context) on error
	}

	// Helper function to log critical startup errors to Windows Event Log if running as a service.
	// Event IDs are examples; you might want to define them as constants.
	logToEventLogIfService := func(eventID uint32, errMsg string) {
		if isService {
			elog, elogErr := eventlog.Open(serviceName) // serviceName is a package-level const
			if elogErr == nil {
				elog.Error(eventID, errMsg)
				elog.Close()
			} else {
				// Fallback: print to stderr if event log fails (though likely lost for service)
				// This message helps if testing event logging itself.
				fmt.Fprintf(os.Stderr, "CRITICAL_ERROR: %s\nAdditionally, failed to write to Event Log for service %s: %v\n", errMsg, serviceName, elogErr)
			}
		}
	}

	var service UptimeMonitorService

	exePath, err := os.Executable()
	if err != nil {
		errMsg := fmt.Sprintf("Could not determine executable path: %v", err)
		logToEventLogIfService(1001, errMsg) // Event ID for exe path error
		log.Fatalln(errMsg)                  // This terminates the application
		return
	}

	workingDir := filepath.Dir(exePath)
	config, err := loadConfig(filepath.Join(workingDir, "config.json"))
	if err != nil {
		errMsg := fmt.Sprintf("Error loading config.json from %s: %v", filepath.Join(workingDir), err)
		logToEventLogIfService(1002, errMsg) // Event ID for config error
		log.Fatalln(errMsg)
		return
	}

	logFilename := fmt.Sprintf("%s.log", time.Now().Format("2006-01"))
	f, err := os.OpenFile(filepath.Join(workingDir, logFilename), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		errMsg := fmt.Sprintf("Error opening log file %s: %v. Ensure the service account has write permissions to the directory.", filepath.Join(workingDir, logFilename), err)
		logToEventLogIfService(1003, errMsg) // Event ID for log file error
		log.Fatalln(errMsg)
	}
	defer f.Close()

	if isService {
		log.SetOutput(f) // Service : log to file
	} else {
		log.SetOutput(os.Stdout) // Console/interactive mode: log to stdout
	}

	service.config = config
	service.eventFile = filepath.Join(workingDir, "events.tmp")

	if len(os.Args) < 2 {
		runService(&service, false)
		return
	}

	switch os.Args[1] {
	case "install":
		installService()
	case "remove":
		removeService()
	case "start":
		startService()
	case "stop":
		stopService()
	case "debug":
		runService(&service, true)
	default:
		fmt.Printf("Commands: install, remove, start, stop, debug\n")
	}
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
