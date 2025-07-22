package logr

import (
    "compress/gzip"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "time"
)

// LogLevel represents the logging level
type LogLevel int

const (
    DEBUG LogLevel = iota
    INFO
    WARN
    ERROR
    FATAL
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
    switch l {
    case DEBUG:
        return "DEBUG"
    case INFO:
        return "INFO"
    case WARN:
        return "WARN"
    case ERROR:
        return "ERROR"
    case FATAL:
        return "FATAL"
    default:
        return "UNKNOWN"
    }
}

// Config represents the logger configuration
type Config struct {
    LogDir       string        // Log directory
    FileName     string        // Log file name prefix
    MaxSize      int64         // Maximum size of a single log file (bytes)
    MaxAge       time.Duration // Log file retention time
    MaxBackups   int           // Maximum number of backup files
    Level        LogLevel      // Log level
    EnableStdout bool          // Whether to output to stdout simultaneously
    SyncInterval time.Duration // Interval for periodic sync (0 means no periodic sync)
    Compress     bool          // Whether to compress rotated log files with gzip
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
    return &Config{
        LogDir:       "./logs",
        FileName:     "dbaudit",
        MaxSize:      100 * 1024 * 1024,  // 100MB
        MaxAge:       7 * 24 * time.Hour, // 7 days
        MaxBackups:   10,
        Level:        INFO,
        EnableStdout: false,
        SyncInterval: 100 * time.Millisecond, // 100ms periodic sync by default
        Compress:     true,                   // Compression by default
    }
}

// Logger represents the logger instance
type Logger struct {
    config      *Config
    file        *os.File
    currentSize int64
    mu          sync.Mutex
    syncTicker  *time.Ticker
    stopChan    chan struct{}
}

// NewLogger creates a new logger instance
func NewLogger(config *Config) (*Logger, error) {
    if config == nil {
        config = DefaultConfig()
    }

    // Ensure log directory exists
    if err := os.MkdirAll(config.LogDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create log directory: %v", err)
    }

    logger := &Logger{
        config:   config,
        stopChan: make(chan struct{}),
    }

    // Open or create log file
    if err := logger.openLogFile(); err != nil {
        return nil, err
    }

    // Start cleanup goroutine
    go logger.cleanupRoutine()

    // Start periodic sync goroutine if enabled
    if config.SyncInterval > 0 {
        logger.syncTicker = time.NewTicker(config.SyncInterval)
        go logger.syncRoutine()
    }

    return logger, nil
}

// openLogFile opens or creates the log file
func (l *Logger) openLogFile() error {
    logPath := l.getCurrentLogPath()

    // Check if file exists, get current size if it does
    if info, err := os.Stat(logPath); err == nil {
        l.currentSize = info.Size()
    } else {
        l.currentSize = 0
    }

    // Open file in append mode
    file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    if err != nil {
        return fmt.Errorf("failed to open log file: %v", err)
    }

    // Close previous file
    if l.file != nil {
        l.file.Close()
    }

    l.file = file
    return nil
}

// getCurrentLogPath gets the current log file path
func (l *Logger) getCurrentLogPath() string {
    return filepath.Join(l.config.LogDir, l.config.FileName+".log")
}

// getBackupLogPath gets the backup log file path
func (l *Logger) getBackupLogPath(timestamp time.Time) string {
    timeStr := timestamp.Format("20060102_150405")
    if l.config.Compress {
        return filepath.Join(l.config.LogDir, fmt.Sprintf("%s_%s.log.gz", l.config.FileName, timeStr))
    }
    return filepath.Join(l.config.LogDir, fmt.Sprintf("%s_%s.log", l.config.FileName, timeStr))
}

// compressFile compresses a log file to gzip format
func (l *Logger) compressFile(srcPath, dstPath string) error {
    // Open source file
    srcFile, err := os.Open(srcPath)
    if err != nil {
        return fmt.Errorf("failed to open source file: %v", err)
    }
    defer srcFile.Close()

    // Create destination gzip file
    dstFile, err := os.Create(dstPath)
    if err != nil {
        return fmt.Errorf("failed to create gzip file: %v", err)
    }
    defer dstFile.Close()

    // Create gzip writer
    gzipWriter := gzip.NewWriter(dstFile)
    defer gzipWriter.Close()

    // Copy file content to gzip
    _, err = io.Copy(gzipWriter, srcFile)
    if err != nil {
        return fmt.Errorf("failed to copy file content: %v", err)
    }

    return nil
}

// rotateFile rotates the log file with proper synchronization
func (l *Logger) rotateFile() error {
    // Store old file reference
    oldFile := l.file

    // Sync and close current file safely
    if oldFile != nil {
        if err := oldFile.Sync(); err != nil {
            // Log sync error but continue with rotation
            fmt.Fprintf(os.Stderr, "Warning: failed to sync log file during rotation: %v\n", err)
        }
        oldFile.Close()
        l.file = nil // Clear reference immediately
    }

    currentPath := l.getCurrentLogPath()
    timestamp := time.Now()

    if l.config.Compress {
        // Compress the current log file
        backupPath := l.getBackupLogPath(timestamp)
        if err := l.compressFile(currentPath, backupPath); err != nil {
            // Try to reopen the original file if compression fails
            l.openLogFile()
            return fmt.Errorf("failed to compress log file: %v", err)
        }

        // Remove the original uncompressed file
        if err := os.Remove(currentPath); err != nil {
            // Log warning but don't fail rotation
            fmt.Fprintf(os.Stderr, "Warning: failed to remove original log file: %v\n", err)
        }
    } else {
        // Rename current file to backup file (original behavior)
        backupPath := l.getBackupLogPath(timestamp)
        if err := os.Rename(currentPath, backupPath); err != nil {
            // Try to reopen the original file if rename fails
            l.openLogFile()
            return fmt.Errorf("failed to rename log file: %v", err)
        }
    }

    // Reset current size
    l.currentSize = 0

    // Open new log file
    if err := l.openLogFile(); err != nil {
        return fmt.Errorf("failed to open new log file after rotation: %v", err)
    }

    return nil
}

// shouldRotate checks if rotation is needed
func (l *Logger) shouldRotate(messageSize int) bool {
    return l.currentSize+int64(messageSize) > l.config.MaxSize
}

// writeLog writes a log message
func (l *Logger) writeLog(level LogLevel, message string) {
    if level < l.config.Level {
        return
    }

    l.mu.Lock()
    defer l.mu.Unlock()

    // Format log message
    timestamp := time.Now().Format("2006-01-02 15:04:05.000")
    logMessage := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level.String(), message)

    // Check if rotation is needed
    if l.shouldRotate(len(logMessage)) {
        if err := l.rotateFile(); err != nil {
            fmt.Fprintf(os.Stderr, "log rotation failed: %v\n", err)
            return
        }
    }

    // Write to file
    if l.file != nil {
        n, err := l.file.WriteString(logMessage)
        if err != nil {
            fmt.Fprintf(os.Stderr, "failed to write to log file: %v\n", err)
            return
        }
        l.currentSize += int64(n)

        // Note: Removed forced sync for better performance
        // Sync will be called during rotation and close operations
    }

    // Also output to stdout
    if l.config.EnableStdout {
        fmt.Print(logMessage)
    }
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(DEBUG, message)
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(INFO, message)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(WARN, message)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(ERROR, message)
}

// Fatal logs a fatal error message and exits the program
func (l *Logger) Fatal(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(FATAL, message)

    // Ensure fatal log is written to disk before exiting
    // Use a separate function to avoid potential deadlock
    l.syncAndExit()
}

// syncAndExit safely syncs the log file and exits
func (l *Logger) syncAndExit() {
    // Try to acquire lock with timeout to prevent hanging
    done := make(chan struct{})
    go func() {
        defer close(done)
        l.mu.Lock()
        defer l.mu.Unlock()
        if l.file != nil {
            l.file.Sync()
        }
    }()

    // Wait for sync with timeout
    select {
    case <-done:
        // Sync completed successfully
    case <-time.After(5 * time.Second):
        // Timeout - force exit to prevent hanging
        fmt.Fprintf(os.Stderr, "Warning: Log sync timed out during fatal exit\n")
    }

    os.Exit(1)
}

// cleanupRoutine is the goroutine for cleaning up expired log files
func (l *Logger) cleanupRoutine() {
    ticker := time.NewTicker(time.Hour) // Check every hour
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            l.cleanup()
        case <-l.stopChan:
            return
        }
    }
}

// syncRoutine is the goroutine for periodic file sync
func (l *Logger) syncRoutine() {
    defer l.syncTicker.Stop()

    for {
        select {
        case <-l.syncTicker.C:
            l.mu.Lock()
            if l.file != nil {
                l.file.Sync()
            }
            l.mu.Unlock()
        case <-l.stopChan:
            return
        }
    }
}

// cleanup removes expired log files
func (l *Logger) cleanup() {
    l.mu.Lock()
    defer l.mu.Unlock()

    // Get all log files
    files, err := l.getLogFiles()
    if err != nil {
        fmt.Fprintf(os.Stderr, "failed to get log file list: %v\n", err)
        return
    }

    now := time.Now()
    deleted := 0

    // Sort by time, newest first
    sort.Slice(files, func(i, j int) bool {
        return files[i].ModTime().After(files[j].ModTime())
    })

    for i, file := range files {
        // Skip the currently active log file
        if file.Name() == l.config.FileName+".log" {
            continue
        }

        shouldDelete := false

        // Check if it exceeds retention time
        if l.config.MaxAge > 0 && now.Sub(file.ModTime()) > l.config.MaxAge {
            shouldDelete = true
        }

        // Check if it exceeds maximum backup count (excluding current file)
        if l.config.MaxBackups > 0 && i >= l.config.MaxBackups {
            shouldDelete = true
        }

        if shouldDelete {
            filePath := filepath.Join(l.config.LogDir, file.Name())
            if err := os.Remove(filePath); err != nil {
                fmt.Fprintf(os.Stderr, "failed to delete expired log file %s: %v\n", filePath, err)
            } else {
                deleted++
            }
        }
    }

    if deleted > 0 {
        fmt.Printf("cleaned up %d expired log files\n", deleted)
    }
}

// getLogFiles gets all log files
func (l *Logger) getLogFiles() ([]os.FileInfo, error) {
    files, err := os.ReadDir(l.config.LogDir)
    if err != nil {
        return nil, err
    }

    var logFiles []os.FileInfo
    prefix := l.config.FileName

    for _, file := range files {
        if file.IsDir() {
            continue
        }

        name := file.Name()
        // Match current log file or backup log files (both .log and .log.gz)
        if name == prefix+".log" ||
            (strings.HasPrefix(name, prefix+"_") && strings.HasSuffix(name, ".log")) ||
            (strings.HasPrefix(name, prefix+"_") && strings.HasSuffix(name, ".log.gz")) {
            info, err := file.Info()
            if err != nil {
                continue
            }
            logFiles = append(logFiles, info)
        }
    }

    return logFiles, nil
}

// Close closes the logger
func (l *Logger) Close() error {
    // Stop background goroutines
    close(l.stopChan)

    l.mu.Lock()
    defer l.mu.Unlock()

    if l.file != nil {
        // Sync before closing to ensure all data is written
        l.file.Sync()
        return l.file.Close()
    }
    return nil
}

// SetLevel sets the log level
func (l *Logger) SetLevel(level LogLevel) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.config.Level = level
}

// GetLevel gets the current log level
func (l *Logger) GetLevel() LogLevel {
    l.mu.Lock()
    defer l.mu.Unlock()
    return l.config.Level
}

// Sync forces a sync of the log file to disk
func (l *Logger) Sync() error {
    l.mu.Lock()
    defer l.mu.Unlock()

    if l.file != nil {
        return l.file.Sync()
    }
    return nil
}

// SetOutput sets additional output target
func (l *Logger) SetOutput(w io.Writer) {
    // This method can be used to add additional output targets, such as network log collectors
    // Here provides a basic implementation framework
}
