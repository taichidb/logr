package logr

import (
    "bufio" // Import bufio for buffered I/O
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

// LogLevel represents the logging level.
type LogLevel int

const (
    DEBUG LogLevel = iota
    INFO
    WARN
    ERROR
    FATAL
)

// String returns the string representation of the log level.
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

// Config represents the logger configuration.
type Config struct {
    LogDir       string        // LogDir is the directory to store log files.
    FileName     string        // FileName is the prefix for log file names.
    MaxSize      int64         // MaxSize is the maximum size of a single log file in bytes.
    MaxAge       time.Duration // MaxAge is the maximum time to retain old log files.
    MaxBackups   int           // MaxBackups is the maximum number of old log files to retain.
    Level        LogLevel      // Level is the logging level.
    EnableStdout bool          // EnableStdout controls whether logs are also written to standard output.
    SyncInterval time.Duration // SyncInterval is the interval for periodic syncs (0 disables periodic syncs).
    Compress     bool          // Compress controls whether rotated log files are compressed with gzip.
}

// DefaultConfig returns the default logger configuration.
func DefaultConfig() *Config {
    return &Config{
        LogDir:       "./logs",
        FileName:     "myapp",
        MaxSize:      100 * 1024 * 1024,  // 100MB
        MaxAge:       7 * 24 * time.Hour, // 7 days
        MaxBackups:   10,
        Level:        INFO,
        EnableStdout: false,
        SyncInterval: 100 * time.Millisecond, // 100ms periodic sync by default
        Compress:     true,                   // Compression enabled by default
    }
}

// Logger represents the logger instance.
type Logger struct {
    config      *Config
    file        *os.File
    writer      *bufio.Writer // Use a buffered writer for performance.
    currentSize int64
    mu          sync.Mutex
    syncTicker  *time.Ticker
    stopChan    chan struct{}
}

// NewLogger creates a new logger instance.
func NewLogger(config *Config) (*Logger, error) {
    if config == nil {
        config = DefaultConfig()
    }

    // Ensure the log directory exists.
    if err := os.MkdirAll(config.LogDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create log directory: %v", err)
    }

    logger := &Logger{
        config:   config,
        stopChan: make(chan struct{}),
    }

    // Open or create the initial log file.
    if err := logger.openLogFile(); err != nil {
        return nil, err
    }

    // Start a goroutine for cleaning up old log files.
    go logger.cleanupRoutine()

    // Start a goroutine for periodic syncing if enabled.
    if config.SyncInterval > 0 {
        logger.syncTicker = time.NewTicker(config.SyncInterval)
        go logger.syncRoutine()
    }

    return logger, nil
}

// openLogFile opens or creates the log file and initializes the buffered writer.
func (l *Logger) openLogFile() error {
    logPath := l.getCurrentLogPath()

    // Get the size of the file if it exists.
    if info, err := os.Stat(logPath); err == nil {
        l.currentSize = info.Size()
    } else if !os.IsNotExist(err) {
        // Return an error if stat fails for a reason other than the file not existing.
        return fmt.Errorf("failed to get log file info: %v", err)
    } else {
        l.currentSize = 0
    }

    // Open the file in append mode.
    file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
    if err != nil {
        return fmt.Errorf("failed to open log file: %v", err)
    }

    l.file = file
    l.writer = bufio.NewWriter(file)
    return nil
}

// getCurrentLogPath returns the path to the current log file.
func (l *Logger) getCurrentLogPath() string {
    return filepath.Join(l.config.LogDir, l.config.FileName+".log")
}

// getBackupLogPath returns the path for a backup log file.
func (l *Logger) getBackupLogPath(timestamp time.Time) string {
    timeStr := timestamp.Format("20060102_150405")
    if l.config.Compress {
        return filepath.Join(l.config.LogDir, fmt.Sprintf("%s_%s.log.gz", l.config.FileName, timeStr))
    }
    return filepath.Join(l.config.LogDir, fmt.Sprintf("%s_%s.log", l.config.FileName, timeStr))
}

// compressFile compresses a log file using gzip.
func (l *Logger) compressFile(srcPath, dstPath string) error {
    srcFile, err := os.Open(srcPath)
    if err != nil {
        return fmt.Errorf("failed to open source file for compression: %v", err)
    }
    defer srcFile.Close()

    dstFile, err := os.Create(dstPath)
    if err != nil {
        return fmt.Errorf("failed to create gzip file: %v", err)
    }
    defer dstFile.Close()

    gzipWriter := gzip.NewWriter(dstFile)
    defer gzipWriter.Close()

    _, err = io.Copy(gzipWriter, srcFile)
    if err != nil {
        return fmt.Errorf("failed to copy file content to gzip writer: %v", err)
    }

    return nil
}

// rotateFile handles log rotation.
func (l *Logger) rotateFile() error {
    // Flush the buffer and close the current file.
    if l.writer != nil {
        if err := l.writer.Flush(); err != nil {
            fmt.Fprintf(os.Stderr, "Warning: failed to flush writer during rotation: %v\n", err)
        }
    }
    if l.file != nil {
        if err := l.file.Close(); err != nil {
            fmt.Fprintf(os.Stderr, "Warning: failed to close file during rotation: %v\n", err)
        }
    }

    // Perform the rotation (rename or compress).
    currentPath := l.getCurrentLogPath()
    timestamp := time.Now()

    if l.config.Compress {
        backupPath := l.getBackupLogPath(timestamp)
        if err := l.compressFile(currentPath, backupPath); err != nil {
            // Try to reopen the original file if compression fails to avoid losing logs.
            l.openLogFile()
            return fmt.Errorf("failed to compress log file: %v", err)
        }
        if err := os.Remove(currentPath); err != nil {
            fmt.Fprintf(os.Stderr, "Warning: failed to remove original log file after compression: %v\n", err)
        }
    } else {
        backupPath := l.getBackupLogPath(timestamp)
        if err := os.Rename(currentPath, backupPath); err != nil {
            // Try to reopen the original file if rename fails.
            l.openLogFile()
            return fmt.Errorf("failed to rename log file: %v", err)
        }
    }

    // Open a new log file. This will also reset the writer and current size.
    return l.openLogFile()
}

// shouldRotate checks if the log file should be rotated based on its size.
func (l *Logger) shouldRotate(messageSize int) bool {
    return l.config.MaxSize > 0 && l.currentSize+int64(messageSize) > l.config.MaxSize
}

// writeLog formats and writes a log message.
func (l *Logger) writeLog(level LogLevel, message string) {
    // Check the log level before doing any work.
    if level < l.config.Level {
        return
    }

    // Format the log message outside the lock to reduce contention.
    timestamp := time.Now().Format("2006-01-02 15:04:05.000")
    logMessage := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level.String(), message)

    l.mu.Lock()
    defer l.mu.Unlock()

    // Check if rotation is needed before writing.
    if l.shouldRotate(len(logMessage)) {
        if err := l.rotateFile(); err != nil {
            fmt.Fprintf(os.Stderr, "log rotation failed: %v\n", err)
            return // Do not write the log if rotation fails.
        }
    }

    // Write to the buffered writer.
    if l.writer != nil {
        n, err := l.writer.WriteString(logMessage)
        if err != nil {
            fmt.Fprintf(os.Stderr, "failed to write to log file: %v\n", err)
            return
        }
        l.currentSize += int64(n)
    }

    // Also write to stdout if enabled.
    if l.config.EnableStdout {
        fmt.Print(logMessage)
    }
}

// Debug logs a message at the DEBUG level.
func (l *Logger) Debug(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(DEBUG, message)
}

// Info logs a message at the INFO level.
func (l *Logger) Info(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(INFO, message)
}

// Warn logs a message at the WARN level.
func (l *Logger) Warn(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(WARN, message)
}

// Error logs a message at the ERROR level.
func (l *Logger) Error(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(ERROR, message)
}

// Fatal logs a message at the FATAL level and then exits the program.
func (l *Logger) Fatal(format string, args ...interface{}) {
    message := fmt.Sprintf(format, args...)
    l.writeLog(FATAL, message)
    l.syncAndExit()
}

// syncAndExit safely flushes, syncs the log file, and then exits.
func (l *Logger) syncAndExit() {
    // Use a separate goroutine with a timeout to prevent hanging on a blocked lock.
    done := make(chan struct{})
    go func() {
        defer close(done)
        l.mu.Lock()
        defer l.mu.Unlock()
        if l.writer != nil {
            l.writer.Flush()
        }
        if l.file != nil {
            l.file.Sync()
        }
    }()

    // Wait for the sync to complete or timeout.
    select {
    case <-done:
        // Sync completed successfully.
    case <-time.After(5 * time.Second):
        // Timeout - force exit to prevent the application from hanging.
        fmt.Fprintf(os.Stderr, "Warning: Log sync timed out during fatal exit\n")
    }

    os.Exit(1)
}

// cleanupRoutine is a goroutine that periodically cleans up old log files.
func (l *Logger) cleanupRoutine() {
    // Check for old files to clean up once per hour.
    ticker := time.NewTicker(time.Hour)
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

// syncRoutine is a goroutine that periodically flushes the log buffer to disk.
func (l *Logger) syncRoutine() {
    defer l.syncTicker.Stop()

    for {
        select {
        case <-l.syncTicker.C:
            l.Sync() // Use the public Sync method.
        case <-l.stopChan:
            return
        }
    }
}

// cleanup removes old log files based on the MaxAge and MaxBackups configuration.
func (l *Logger) cleanup() {
    l.mu.Lock()
    defer l.mu.Unlock()

    files, err := l.getLogFiles()
    if err != nil {
        fmt.Fprintf(os.Stderr, "failed to get log file list for cleanup: %v\n", err)
        return
    }

    now := time.Now()
    deleted := 0

    // Sort files by modification time, newest first.
    sort.Slice(files, func(i, j int) bool {
        return files[i].ModTime().After(files[j].ModTime())
    })

    for i, file := range files {
        // Skip the currently active log file.
        if file.Name() == l.config.FileName+".log" {
            continue
        }

        shouldDelete := false
        // Check if the file is older than the configured max age.
        if l.config.MaxAge > 0 && now.Sub(file.ModTime()) > l.config.MaxAge {
            shouldDelete = true
        }

        // Check if the number of backup files exceeds the configured limit.
        if l.config.MaxBackups > 0 && i >= l.config.MaxBackups {
            shouldDelete = true
        }

        if shouldDelete {
            filePath := filepath.Join(l.config.LogDir, file.Name())
            if err := os.Remove(filePath); err != nil {
                fmt.Fprintf(os.Stderr, "failed to delete old log file %s: %v\n", filePath, err)
            } else {
                deleted++
            }
        }
    }

    if deleted > 0 {
        // This message could be logged via the logger itself at a DEBUG level
        // if further enhancements are made.
        fmt.Printf("cleaned up %d old log files\n", deleted)
    }
}

// getLogFiles returns a list of all log files matching the logger's file name prefix.
func (l *Logger) getLogFiles() ([]os.FileInfo, error) {
    dirEntries, err := os.ReadDir(l.config.LogDir)
    if err != nil {
        return nil, err
    }

    var logFiles []os.FileInfo
    prefix := l.config.FileName

    for _, entry := range dirEntries {
        if entry.IsDir() {
            continue
        }

        name := entry.Name()
        // Match the current log file or backup files (both .log and .log.gz).
        if name == prefix+".log" ||
            (strings.HasPrefix(name, prefix+"_") && (strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz"))) {
            info, err := entry.Info()
            if err != nil {
                continue // Skip files we can't get info for.
            }
            logFiles = append(logFiles, info)
        }
    }

    return logFiles, nil
}

// Close closes the logger, ensuring all buffered logs are written to disk.
func (l *Logger) Close() error {
    // Stop background goroutines.
    close(l.stopChan)

    l.mu.Lock()
    defer l.mu.Unlock()

    if l.writer != nil {
        l.writer.Flush()
    }
    if l.file != nil {
        l.file.Sync()
        return l.file.Close()
    }
    return nil
}

// SetLevel dynamically changes the logging level.
func (l *Logger) SetLevel(level LogLevel) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.config.Level = level
}

// GetLevel returns the current logging level.
func (l *Logger) GetLevel() LogLevel {
    l.mu.Lock()
    defer l.mu.Unlock()
    return l.config.Level
}

// Sync forces a flush of the log buffer to the underlying file and syncs it to disk.
func (l *Logger) Sync() error {
    l.mu.Lock()
    defer l.mu.Unlock()

    if l.writer != nil {
        if err := l.writer.Flush(); err != nil {
            return err
        }
    }
    if l.file != nil {
        return l.file.Sync()
    }
    return nil
}

// SetOutput is a placeholder for setting an additional output writer.
// A more complete implementation would use io.MultiWriter.
func (l *Logger) SetOutput(w io.Writer) {
    // This method can be used to add additional output targets, such as network log collectors.
    // A complete implementation would likely involve using an io.MultiWriter
    // to write to both the file and the provided io.Writer.
}
