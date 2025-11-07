# go-psql-bckp

# PostgreSQL System Tray Monitor - Development Session Summary

## Project Overview
Created a Go-based system tray application for monitoring a remote PostgreSQL server with backup capabilities.

---

## Features Implemented

### 1. **Core Monitoring**
- Real-time PostgreSQL server connection monitoring
- Checks every 30 seconds automatically
- Visual status indicators (green/red icons)
- Displays:
  - Connection status (Connected/Disconnected)
  - Active connections count
  - Database uptime
  - Last check timestamp

### 2. **Backup Functionality**
- **Single Database Backup** - Uses `pg_dump`
- **Full Server Backup** - Uses `pg_dumpall` for all databases
- Backup files prefixed with `vindija-bl_`
- Timestamped filenames (format: `YYYYMMDD_HHMMSS`)
- Stored in `./backups/` directory
- Shows file size after completion

### 3. **Scheduled Backups**
- Automatic daily backups at configurable time
- 24-hour time format (e.g., "02:00" for 2 AM)
- Countdown timer showing next backup
- Choose between single DB or all databases backup
- Automatically recalculates next backup time

### 4. **Cloud Integration**
- Upload backups to Nextcloud via WebDAV
- Uses `curl` for file transfer
- Automatic upload after successful backup
- Status indicators: "(cloud)", "(local only)", or "Failed"

### 5. **Configuration Management**
- External `config.json` file for all settings
- Secure file permissions (0600 - owner read/write only)
- Auto-generates default config on first run
- No hardcoded credentials

### 6. **Logging**
- All operations logged to `pg-monitor.log`
- Detailed error messages
- Backup progress tracking
- Connection diagnostics

---

## Technical Details

### Dependencies
```bash
go get github.com/getlantern/systray
go get github.com/lib/pq
```

### External Requirements
- `pg_dump` (PostgreSQL client tools) - for single database backups
- `pg_dumpall` (PostgreSQL client tools) - for full server backups
- `curl` (optional) - for Nextcloud uploads

### Configuration File (`config.json`)
```json
{
  "Host": "localhost",
  "Port": 5432,
  "User": "postgres",
  "Password": "your_password",
  "DBName": "your_database",
  "NextcloudURL": "https://cloud.example.com/remote.php/dav/files/username/backups/",
  "NextcloudUser": "nextcloud_username",
  "NextcloudPass": "nextcloud_password",
  "UploadToCloud": false,
  "AutoBackupEnabled": true,
  "AutoBackupTime": "02:00",
  "AutoBackupAll": true
}
```

---

## Build Instructions

### Standard Build
```bash
go build -o pg-monitor.exe
```

### Windows GUI Build (no console window)
```bash
go build -ldflags -H=windowsgui -o pg-monitor.exe
```

### Run
```bash
.\pg-monitor.exe
```

---

## Issues Resolved

### 1. **Context Import Missing**
- **Problem**: `time.WithTimeout` and `time.Background` undefined
- **Solution**: Added `"context"` to imports

### 2. **pg_dump Version Mismatch**
- **Problem**: Server version 17.6, pg_dump version 12.1
- **Error**: `pg_dump: error: server version: 17.6; pg_dump version: 12.1`
- **Solution**: Switched from custom format (`-F c`) to plain SQL format for better compatibility
- **Recommendation**: Upgrade pg_dump to version 17

### 3. **Zero-Byte Backup Files**
- **Problem**: Backups created but empty (0 bytes)
- **Solution**: 
  - Added detailed error logging with stderr capture
  - Automatic cleanup of failed backup files
  - Better error messages in tooltip and logs

### 4. **Function Signature Mismatch**
- **Problem**: `backupDatabase()` called with arguments but defined without parameters
- **Solution**: Full rewrite to ensure `backupDatabase(allDatabases bool)` signature consistent

### 5. **Icons Not Displaying**
- **Problem**: Complex PostgreSQL elephant icons not rendering in system tray
- **Solution**: Replaced with simpler 16x16 PNG icons (green/red circles)

---

## File Structure
```
project/
├── pg-monitor.exe          # Compiled executable
├── config.json             # Configuration file (auto-generated)
├── pg-monitor.log          # Application logs
└── backups/                # Backup directory (auto-created)
    ├── vindija-bl_dbname_backup_20241105_143025.sql
    └── vindija-bl_all_databases_backup_20241105_143030.sql
```

---

## System Tray Menu Structure
```
Status: ✓ Connected
Active Connections: 5
DB Uptime: 7 days 12:34:56
Last Check: 14:30:25
─────────────────────
Last Backup: 2 hours ago (450.23 KB cloud)
Next Backup: in 10 hours (All DBs)
─────────────────────
Refresh Now
Backup Database
Backup All Databases
─────────────────────
Quit
```

---

## Key Features Summary

| Feature | Status | Notes |
|---------|--------|-------|
| Connection Monitoring | ✅ | Every 30 seconds |
| Visual Status Icons | ✅ | Green (connected) / Red (disconnected) |
| Active Connections Count | ✅ | Real-time query |
| Database Uptime | ✅ | PostgreSQL system query |
| Manual Single DB Backup | ✅ | pg_dump |
| Manual Full Server Backup | ✅ | pg_dumpall |
| Scheduled Daily Backups | ✅ | Configurable time |
| Nextcloud Upload | ✅ | Optional, via curl |
| Config File | ✅ | JSON format |
| Logging | ✅ | File-based |
| Backup File Prefix | ✅ | `vindija-bl_` |
| Error Handling | ✅ | Comprehensive |

---

## Usage Workflow

1. **First Run**
   - Application creates `config.json` with defaults
   - Edit config with actual credentials
   - Restart application

2. **Normal Operation**
   - App sits in system tray
   - Monitors connection every 30 seconds
   - Updates icon color based on connection status
   - Runs scheduled backups automatically

3. **Manual Backups**
   - Right-click tray icon
   - Select "Backup Database" or "Backup All Databases"
   - Check tooltip for completion status
   - Files saved in `./backups/` directory

4. **Monitoring Logs**
   - Check `pg-monitor.log` for detailed information
   - All operations timestamped
   - Error messages with full details

---

## Security Considerations

- Configuration file has 0600 permissions (owner only)
- Passwords stored in config file (consider encryption for production)
- PGPASSWORD environment variable used temporarily during backup
- Nextcloud credentials transmitted via basic auth over HTTPS

---

## Future Enhancement Ideas

- [ ] Config file encryption
- [ ] Multiple server monitoring
- [ ] Email notifications on failures
- [ ] Backup retention policy (auto-delete old backups)
- [ ] Database size monitoring
- [ ] Query performance metrics
- [ ] Windows service mode
- [ ] GUI configuration editor
- [ ] Backup compression options
- [ ] S3/Azure Blob Storage support

---

## Version History

**v1.0** - Initial implementation with:
- Connection monitoring
- Manual & scheduled backups
- Nextcloud integration
- Config file support
- Comprehensive logging
