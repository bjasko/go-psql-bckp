package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/getlantern/systray"
	_ "github.com/lib/pq"
)

const (
	checkInterval = 30 * time.Second
	connTimeout   = 5 * time.Second
)

type Config struct {
	Host              string
	Port              int
	User              string
	Password          string
	DBName            string
	NextcloudURL      string // e.g., https://cloud.example.com/remote.php/dav/files/username/backups/
	NextcloudUser     string
	NextcloudPass     string
	UploadToCloud     bool
	AutoBackupEnabled bool
	AutoBackupTime    string // Format: "15:04" (24-hour time, e.g., "02:30" for 2:30 AM)
	AutoBackupAll     bool   // true = backup all databases, false = backup single database
}

type Monitor struct {
	config            Config
	db                *sql.DB
	statusItem        *systray.MenuItem
	uptimeItem        *systray.MenuItem
	connsItem         *systray.MenuItem
	lastCheck         *systray.MenuItem
	lastBackupItem    *systray.MenuItem
	nextBackupItem    *systray.MenuItem
	backupItem        *systray.MenuItem
	backupAllItem     *systray.MenuItem
	isConnected       bool
	startTime         time.Time
	lastBackupTime    time.Time
	lastBackupStatus  string
	nextScheduledTime time.Time
}

func main() {
	// Setup logging to file
	logFile, err := os.OpenFile("pg-monitor.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}
	log.Printf("=== PostgreSQL Monitor Started ===")

	// Load configuration from file
	config, err := loadConfig("config.json")
	if err != nil {
		log.Printf("Error loading config: %v", err)
		log.Printf("Creating default config.json file...")

		// Create default config
		defaultConfig := Config{
			Host:              "localhost",
			Port:              5432,
			User:              "postgres",
			Password:          "your_password",
			DBName:            "your_database",
			NextcloudURL:      "", // e.g., "https://cloud.example.com/remote.php/dav/files/username/backups/"
			NextcloudUser:     "",
			NextcloudPass:     "",
			UploadToCloud:     false,
			AutoBackupEnabled: true,
			AutoBackupTime:    "02:00",
			AutoBackupAll:     true,
		}

		if err := saveConfig("config.json", defaultConfig); err != nil {
			log.Fatalf("Failed to create config file: %v", err)
		}

		log.Printf("Default config.json created. Please edit it with your settings and restart.")
		config = defaultConfig
	}

	monitor := &Monitor{
		config:    config,
		startTime: time.Now(),
	}

	systray.Run(monitor.onReady, monitor.onExit)
}

func loadConfig(filename string) (Config, error) {
	var config Config

	data, err := os.ReadFile(filename)
	if err != nil {
		return config, err
	}

	err = json.Unmarshal(data, &config)
	return config, err
}

func saveConfig(filename string, config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filename, data, 0600) // 0600 = read/write for owner only (secure)
}

func (m *Monitor) onReady() {
	systray.SetIcon(getIcon(false))
	systray.SetTitle("PG Monitor")
	systray.SetTooltip("PostgreSQL Monitor")

	m.statusItem = systray.AddMenuItem("Status: Checking...", "Current connection status")
	m.statusItem.Disable()

	m.connsItem = systray.AddMenuItem("Active Connections: -", "Number of active connections")
	m.connsItem.Disable()

	m.uptimeItem = systray.AddMenuItem("Uptime: -", "Database uptime")
	m.uptimeItem.Disable()

	m.lastCheck = systray.AddMenuItem("Last Check: -", "Last check timestamp")
	m.lastCheck.Disable()

	systray.AddSeparator()

	m.lastBackupItem = systray.AddMenuItem("Last Backup: Never", "Last successful backup")
	m.lastBackupItem.Disable()

	m.nextBackupItem = systray.AddMenuItem("Next Backup: -", "Next scheduled backup")
	m.nextBackupItem.Disable()

	systray.AddSeparator()

	refreshItem := systray.AddMenuItem("Refresh Now", "Check database status now")
	m.backupItem = systray.AddMenuItem("Backup Database", "Create database backup")
	m.backupAllItem = systray.AddMenuItem("Backup All Databases", "Create full server backup")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit", "Exit the application")

	// Initial check
	go m.checkDatabase()

	// Start monitoring loop
	go m.monitorLoop()

	// Start scheduled backup scheduler
	if m.config.AutoBackupEnabled {
		go m.scheduleBackups()
	}

	// Handle menu clicks
	go func() {
		for {
			select {
			case <-refreshItem.ClickedCh:
				go m.checkDatabase()
			case <-m.backupItem.ClickedCh:
				go m.backupDatabase(false)
			case <-m.backupAllItem.ClickedCh:
				go m.backupDatabase(true)
			case <-quitItem.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func (m *Monitor) monitorLoop() {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for range ticker.C {
		m.checkDatabase()
	}
}

func (m *Monitor) scheduleBackups() {
	log.Printf("Scheduled backups enabled at %s", m.config.AutoBackupTime)

	for {
		now := time.Now()
		nextRun := m.calculateNextBackupTime(now)
		m.nextScheduledTime = nextRun
		m.updateNextBackupStatus()

		duration := time.Until(nextRun)
		log.Printf("Next scheduled backup in %v (at %s)", duration, nextRun.Format("2006-01-02 15:04:05"))

		timer := time.NewTimer(duration)
		<-timer.C

		log.Printf("Running scheduled backup...")
		m.backupDatabase(m.config.AutoBackupAll)

		// Update next backup time after completion
		m.nextScheduledTime = m.calculateNextBackupTime(time.Now())
		m.updateNextBackupStatus()
	}
}

func (m *Monitor) calculateNextBackupTime(from time.Time) time.Time {
	// Parse the configured time
	targetTime, err := time.Parse("15:04", m.config.AutoBackupTime)
	if err != nil {
		log.Printf("Invalid backup time format: %v, using 02:00", err)
		targetTime, _ = time.Parse("15:04", "02:00")
	}

	// Set the target time for today
	nextRun := time.Date(from.Year(), from.Month(), from.Day(),
		targetTime.Hour(), targetTime.Minute(), 0, 0, from.Location())

	// If the time has already passed today, schedule for tomorrow
	if nextRun.Before(from) || nextRun.Equal(from) {
		nextRun = nextRun.Add(24 * time.Hour)
	}

	return nextRun
}

func (m *Monitor) updateNextBackupStatus() {
	if !m.config.AutoBackupEnabled {
		m.nextBackupItem.SetTitle("Next Backup: Disabled")
		return
	}

	if m.nextScheduledTime.IsZero() {
		m.nextBackupItem.SetTitle("Next Backup: Calculating...")
		return
	}

	until := time.Until(m.nextScheduledTime)
	var timeStr string

	if until < time.Minute {
		timeStr = "in < 1 min"
	} else if until < time.Hour {
		timeStr = fmt.Sprintf("in %d min", int(until.Minutes()))
	} else if until < 24*time.Hour {
		timeStr = fmt.Sprintf("in %d hours", int(until.Hours()))
	} else {
		days := int(until.Hours() / 24)
		hours := int(until.Hours()) % 24
		timeStr = fmt.Sprintf("in %dd %dh", days, hours)
	}

	backupType := "DB"
	if m.config.AutoBackupAll {
		backupType = "All DBs"
	}

	m.nextBackupItem.SetTitle(fmt.Sprintf("Next Backup: %s (%s)", timeStr, backupType))
}

func (m *Monitor) checkDatabase() {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable connect_timeout=%d",
		m.config.Host, m.config.Port, m.config.User, m.config.Password, m.config.DBName, int(connTimeout.Seconds()))

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		m.updateStatus(false, err)
		return
	}
	defer db.Close()

	db.SetConnMaxLifetime(connTimeout)
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), connTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		m.updateStatus(false, err)
		return
	}

	// Get active connections
	var activeConns int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE state = 'active'").Scan(&activeConns)
	if err != nil {
		log.Printf("Error getting active connections: %v", err)
		activeConns = -1
	}

	// Get database uptime
	var uptime string
	err = db.QueryRowContext(ctx, "SELECT NOW() - pg_postmaster_start_time()").Scan(&uptime)
	if err != nil {
		log.Printf("Error getting uptime: %v", err)
		uptime = "unknown"
	}

	m.updateStatus(true, nil)
	m.updateMetrics(activeConns, uptime)
}

func (m *Monitor) updateStatus(connected bool, err error) {
	m.isConnected = connected

	if connected {
		systray.SetIcon(getIcon(true))
		systray.SetTooltip("PostgreSQL Monitor - Connected")
		m.statusItem.SetTitle("Status: ✓ Connected")
	} else {
		systray.SetIcon(getIcon(false))
		systray.SetTooltip(fmt.Sprintf("PostgreSQL Monitor - Disconnected: %v", err))
		m.statusItem.SetTitle("Status: ✗ Disconnected")
		m.connsItem.SetTitle("Active Connections: -")
		m.uptimeItem.SetTitle("Uptime: -")
	}

	m.lastCheck.SetTitle(fmt.Sprintf("Last Check: %s", time.Now().Format("15:04:05")))
}

func (m *Monitor) updateMetrics(activeConns int, uptime string) {
	if activeConns >= 0 {
		m.connsItem.SetTitle(fmt.Sprintf("Active Connections: %d", activeConns))
	}
	m.uptimeItem.SetTitle(fmt.Sprintf("DB Uptime: %s", formatUptime(uptime)))
}

func (m *Monitor) onExit() {
	if m.db != nil {
		m.db.Close()
	}
}

func (m *Monitor) backupDatabase(allDatabases bool) {
	m.backupItem.SetTitle("Backup Database (Running...)")
	m.backupItem.Disable()
	if allDatabases {
		m.backupAllItem.SetTitle("Backup All Databases (Running...)")
		m.backupAllItem.Disable()
	}
	defer func() {
		m.backupItem.SetTitle("Backup Database")
		m.backupItem.Enable()
		if allDatabases {
			m.backupAllItem.SetTitle("Backup All Databases")
			m.backupAllItem.Enable()
		}
	}()

	timestamp := time.Now().Format("20060102_150405")
	backupDir := filepath.Join(".", "backups")

	// Create backups directory if it doesn't exist
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		errMsg := fmt.Sprintf("Failed to create backup directory: %v", err)
		log.Printf(errMsg)
		systray.SetTooltip(errMsg)
		return
	}

	var backupFile string
	var cmd *exec.Cmd

	// Set password in environment
	env := os.Environ()
	env = append(env, fmt.Sprintf("PGPASSWORD=%s", m.config.Password))

	if allDatabases {
		// Full server backup using pg_dumpall
		backupFile = filepath.Join(backupDir, fmt.Sprintf("vindija-bl_all_databases_backup_%s.sql", timestamp))
		log.Printf("Starting full server backup to: %s", backupFile)

		cmd = exec.Command("pg_dumpall",
			"-h", m.config.Host,
			"-p", fmt.Sprintf("%d", m.config.Port),
			"-U", m.config.User,
			"-f", backupFile,
		)
	} else {
		// Single database backup
		backupFile = filepath.Join(backupDir, fmt.Sprintf("vindija-bl_%s_backup_%s.sql", m.config.DBName, timestamp))
		log.Printf("Starting backup to: %s", backupFile)

		cmd = exec.Command("pg_dump",
			"-h", m.config.Host,
			"-p", fmt.Sprintf("%d", m.config.Port),
			"-U", m.config.User,
			"-f", backupFile,
			m.config.DBName,
		)
	}

	log.Printf("Connection: host=%s port=%d user=%s", m.config.Host, m.config.Port, m.config.User)
	systray.SetTooltip("Creating database backup...")

	cmd.Env = env

	// Capture stdout and stderr separately
	var stdout, stderr []byte
	var err error

	stdout, err = cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = exitErr.Stderr
		}
		errMsg := fmt.Sprintf("Backup failed: %v\nStderr: %s\nStdout: %s", err, string(stderr), string(stdout))
		log.Printf(errMsg)
		systray.SetTooltip(fmt.Sprintf("Backup failed - check console"))

		// Clean up empty file
		os.Remove(backupFile)
		m.lastBackupStatus = "Failed"
		m.updateBackupStatus()
		return
	}

	log.Printf("Backup output: %s", string(stdout))

	// Check file was created and has content
	if info, err := os.Stat(backupFile); err == nil {
		if info.Size() == 0 {
			log.Printf("WARNING: Backup file is empty (0 bytes)")
			systray.SetTooltip("Backup failed: file is empty")
			os.Remove(backupFile)
			m.lastBackupStatus = "Failed (empty file)"
			m.updateBackupStatus()
			return
		}
		sizeKB := float64(info.Size()) / 1024.0
		successMsg := fmt.Sprintf("Backup complete: %.2f KB", sizeKB)
		log.Printf("Backup completed successfully: %s (%.2f KB)", backupFile, sizeKB)

		// Upload to Nextcloud if configured
		if m.config.UploadToCloud && m.config.NextcloudURL != "" {
			log.Printf("Uploading to Nextcloud...")
			systray.SetTooltip("Uploading backup to Nextcloud...")
			if err := m.uploadToNextcloud(backupFile); err != nil {
				log.Printf("Nextcloud upload failed: %v", err)
				systray.SetTooltip(fmt.Sprintf("Backup saved locally (%.2f KB), upload failed", sizeKB))
				m.lastBackupStatus = fmt.Sprintf("%.2f KB (local only)", sizeKB)
			} else {
				log.Printf("Successfully uploaded to Nextcloud")
				systray.SetTooltip(fmt.Sprintf("Backup complete: %.2f KB (uploaded to cloud)", sizeKB))
				m.lastBackupStatus = fmt.Sprintf("%.2f KB (cloud)", sizeKB)
			}
		} else {
			systray.SetTooltip(successMsg)
			m.lastBackupStatus = fmt.Sprintf("%.2f KB", sizeKB)
		}

		// Update last backup info
		m.lastBackupTime = time.Now()
		m.updateBackupStatus()

		// Update next backup time if this was a scheduled backup
		if m.config.AutoBackupEnabled {
			m.nextScheduledTime = m.calculateNextBackupTime(time.Now())
			m.updateNextBackupStatus()
		}
	} else {
		log.Printf("Backup file not found: %v", err)
		systray.SetTooltip("Backup status unclear - check logs")
		m.lastBackupStatus = "Status unclear"
		m.updateBackupStatus()
	}
}

func (m *Monitor) uploadToNextcloud(filePath string) error {
	fileName := filepath.Base(filePath)
	uploadURL := m.config.NextcloudURL + fileName

	log.Printf("Uploading to: %s", uploadURL)

	// Prepare curl command
	cmd := exec.Command("curl",
		"-X", "PUT",
		"-u", fmt.Sprintf("%s:%s", m.config.NextcloudUser, m.config.NextcloudPass),
		"--data-binary", "@"+filePath,
		uploadURL,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("curl failed: %v, output: %s", err, string(output))
	}

	log.Printf("Upload response: %s", string(output))
	return nil
}

func (m *Monitor) updateBackupStatus() {
	if m.lastBackupTime.IsZero() {
		m.lastBackupItem.SetTitle("Last Backup: Never")
	} else {
		elapsed := time.Since(m.lastBackupTime)
		var timeStr string

		if elapsed < time.Minute {
			timeStr = "just now"
		} else if elapsed < time.Hour {
			timeStr = fmt.Sprintf("%d min ago", int(elapsed.Minutes()))
		} else if elapsed < 24*time.Hour {
			timeStr = fmt.Sprintf("%d hours ago", int(elapsed.Hours()))
		} else {
			timeStr = fmt.Sprintf("%d days ago", int(elapsed.Hours()/24))
		}

		m.lastBackupItem.SetTitle(fmt.Sprintf("Last Backup: %s (%s)", timeStr, m.lastBackupStatus))
	}
}

func formatUptime(uptime string) string {
	// PostgreSQL returns interval format, simplify it
	if len(uptime) > 20 {
		return uptime[:20] + "..."
	}
	return uptime
}

func getIcon(connected bool) []byte {
	if connected {
		// PostgreSQL elephant icon - blue (connected)
		return []byte{
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
			0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x20,
			0x08, 0x06, 0x00, 0x00, 0x00, 0x73, 0x7a, 0x7a, 0xf4, 0x00, 0x00, 0x01,
			0x84, 0x49, 0x44, 0x41, 0x54, 0x58, 0x85, 0xed, 0x97, 0x4b, 0x4a, 0x03,
			0x41, 0x10, 0x86, 0xbf, 0xec, 0xee, 0x2e, 0x08, 0x82, 0x20, 0x48, 0x90,
			0x90, 0x07, 0x08, 0x71, 0x03, 0x97, 0x5e, 0xc2, 0xc5, 0x4b, 0xf0, 0x06,
			0x82, 0x17, 0x10, 0xbc, 0x83, 0xe0, 0x11, 0x3c, 0x81, 0xd7, 0xf0, 0x00,
			0x97, 0x88, 0x88, 0x24, 0x81, 0x24, 0x26, 0xbb, 0xab, 0xba, 0xfb, 0xd1,
			0xd3, 0x5d, 0x55, 0xf5, 0x54, 0x57, 0x4d, 0xd5, 0x4c, 0xf7, 0x9b, 0x01,
			0x18, 0x00, 0x18, 0x30, 0x60, 0xc0, 0x80, 0x01, 0x03, 0x06, 0x0c, 0x18,
			0x30, 0x60, 0xc0, 0x80, 0x01, 0x03, 0x86, 0x4c, 0x98, 0x73, 0xce, 0xe7,
			0x9c, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53,
			0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39,
			0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94,
			0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e,
			0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5,
			0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53,
			0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39,
			0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94,
			0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e,
			0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5,
			0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53,
			0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39,
			0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94,
			0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e,
			0x39, 0xe5, 0x94, 0x53, 0xce, 0x7f, 0x04, 0x3c, 0x00, 0xbc, 0x48, 0x43,
			0x8f, 0xbd, 0xba, 0x85, 0xee, 0x4e, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
			0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
		}
	}
	// PostgreSQL elephant icon - gray (disconnected)
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x20, 0x00, 0x00, 0x00, 0x20,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x73, 0x7a, 0x7a, 0xf4, 0x00, 0x00, 0x01,
		0x54, 0x49, 0x44, 0x41, 0x54, 0x58, 0x85, 0xed, 0x97, 0x4b, 0x0a, 0xc2,
		0x40, 0x10, 0x86, 0xbf, 0xd9, 0xdd, 0x5d, 0x50, 0x04, 0x41, 0x90, 0xa0,
		0x3e, 0x40, 0x88, 0x1b, 0x78, 0x15, 0x2f, 0xe1, 0xe2, 0x25, 0xf8, 0x06,
		0x82, 0x57, 0x10, 0xbc, 0x83, 0xe0, 0x11, 0x3c, 0x81, 0xd7, 0xf0, 0x00,
		0x97, 0x88, 0x88, 0xa4, 0x81, 0xa4, 0x26, 0xbb, 0xab, 0xba, 0x7b, 0xd1,
		0xdd, 0x5d, 0x55, 0xf5, 0x54, 0x57, 0x4d, 0xd5, 0x4c, 0xf7, 0x9b, 0x19,
		0x60, 0x00, 0x18, 0x00, 0x06, 0x80, 0x01, 0x60, 0x00, 0x18, 0x00, 0x06,
		0x80, 0x01, 0x60, 0x00, 0x18, 0x00, 0x86, 0x4c, 0x98, 0x73, 0xce, 0xe7,
		0x9c, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53,
		0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39,
		0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94,
		0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e,
		0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5,
		0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53,
		0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39,
		0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94,
		0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e,
		0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5,
		0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53,
		0x4e, 0x39, 0xe5, 0x94, 0x53, 0x4e, 0x39, 0xe5, 0x94, 0x53, 0xce, 0x3f,
		0x04, 0x3c, 0x00, 0xbc, 0x48, 0x43, 0x8f, 0xbd, 0x8a, 0x5c, 0xa0, 0x84,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
}
