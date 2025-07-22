package logr

import (
    "bufio"
    "bytes"
    "compress/gzip"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "sync"
    "time"
)

// bufferPool is a pool of bytes.Buffer objects to reduce memory allocations.
var bufferPool = sync.Pool{
    New: func() interface{} {
        return &bytes.Buffer{}
    },
}

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
    EnableStdout bool          // EnableStdout is a convenience option to log to os.Stdout.
    SyncInterval time.Duration // SyncInterval is the interval for periodic syncs (0 disables periodic syncs).
    Compress     bool          // Compress controls whether rotated log files are compressed with gzip.
    JSONFormat   bool          // JSONFormat controls whether logs are formatted as JSON.
    Output       io.Writer     // Output allows specifying a custom writer. It overrides EnableStdout if set.
    ErrorHandler func(error)   // ErrorHandler is a callback for handling internal logger errors.
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
        JSONFormat:   false,
        Output:       nil,
        ErrorHandler: nil,
    }
}

// Logger represents the logger instance.
type Logger struct {
    config      *Config
    file        *os.File
    out         io.Writer // Combined output writer (file + custom/stdout)
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

// openLogFile opens or creates the log file and sets up the output writers.
func (l *Logger) openLogFile() error {
    logPath := l.getCurrentLogPath()

    // Get the size of the file if it exists.
    if info, err := os.Stat(logPath); err == nil {
        l.currentSize = info.Size()
    } else if !os.IsNotExist(err) {
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

    // Set up the multi-writer.
    fileWriter := bufio.NewWriter(file)
    outputs := []io.Writer{fileWriter}

    if l.config.Output != nil {
        outputs = append(outputs, l.config.Output)
    } else if l.config.EnableStdout {
        outputs = append(outputs, os.Stdout)
    }
    l.out = io.MultiWriter(outputs...)

    return nil
}

// handleError provides a centralized way to handle internal errors.
func (l *Logger) handleError(err error) {
    if l.config.ErrorHandler != nil {
        l.config.ErrorHandler(err)
    } else {
        fmt.Fprintf(os.Stderr, "logr error: %v\n", err)
    }
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
    if f, ok := l.out.(*io.MultiWriter); ok {
        for _, w := range getWriters(f) {
            if b, ok := w.(*bufio.Writer); ok {
                if err := b.Flush(); err != nil {
                    l.handleError(fmt.Errorf("failed to flush writer during rotation: %v", err))
                }
            }
        }
    }
    if l.file != nil {
        if err := l.file.Close(); err != nil {
            l.handleError(fmt.Errorf("failed to close file during rotation: %v", err))
        }
    }

    // Perform the rotation (rename or compress).
    currentPath := l.getCurrentLogPath()
    timestamp := time.Now()

    if l.config.Compress {
        backupPath := l.getBackupLogPath(timestamp)
        if err := l.compressFile(currentPath, backupPath); err != nil {
            l.openLogFile()
            return fmt.Errorf("failed to compress log file: %v", err)
        }!
        if err := os.Remove(currentPath); err != nil {
            l.handleError(fmt.Errorf("failed to remove original log file after compression: %v", err))
        }
    } else {
        backupPath := l.getBackupLogPath(timestamp)
        if err := os.Rename(currentPath, backupPath); err != nil {
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
func (l *Logger) writeLog(level LogLevel, format string, args ...interface{}) {
    if level < l.config.Level {
        return
    }

    buf := bufferPool.Get().(*bytes.Buffer)
    buf.Reset()
    defer bufferPool.Put(buf)

    if l.config.JSONFormat {
        // JSON format
        logEntry := map[string]interface{}{
            "time":    time.Now().Format(time.RFC3339Nano),
            "level":   level.String(),
            "message": fmt.Sprintf(format, args...),
        }
        if err := json.NewEncoder(buf).Encode(logEntry); err != nil {
            l.handleError(fmt.Errorf("failed to encode JSON log entry: %v", err))
            return
        }
    } else {
        // Plain text format
        timestamp := time.Now().Format("2006-01-02 15:04:05.000")
        fmt.Fprintf(buf, "[%s] [%s] ", timestamp, level.String())
        fmt.Fprintf(buf, format, args...)
        buf.WriteByte('\n')
    }

    l.mu.Lock()
    defer l.mu.Unlock()

    if l.shouldRotate(buf.Len()) {
        if err := l.rotateFile(); err != nil {
            l.handleError(fmt.Errorf("log rotation failed: %v", err))
            return
        }
    }

    if l.out != nil {
        n, err := l.out.Write(buf.Bytes())
        if err != nil {
            l.handleError(fmt.Errorf("failed to write to log output: %v", err))
            return
        }
        // We only track the size written to the main file, not other outputs.
        // This is a simplification. A more complex setup might track it differently.
        l.currentSize += int64(n)
    }
}

// Debug logs a message at the DEBUG level.
func (l *Logger) Debug(format string, args ...interface{}) {
    l.writeLog(DEBUG, format, args...)
}

// Info logs a message at the INFO level.
func (l *Logger) Info(format string, args ...interface{}) {
    l.writeLog(INFO, format, args...)
}

// Warn logs a message at the WARN level.
func (l *Logger) Warn(format string, args ...interface{}) {
    l.writeLog(WARN, format, args...)
}

// Error logs a message at the ERROR level.
func (l *Logger) Error(format string, args ...interface{}) {
    l.writeLog(ERROR, format, args...)
}

// Fatal logs a message at the FATAL level and then exits the program.
func (l *Logger) Fatal(format string, args ...interface{}) {
    l.writeLog(FATAL, format, args...)
    l.syncAndExit()
}

// syncAndExit safely flushes, syncs the log file, and then exits.
func (l *Logger) syncAndExit() {
    done := make(chan struct{})
    go func() {
        defer close(done)
        l.Sync()
    }()

    select {
    case <-done:
        // Sync completed.
    case <-time.After(5 * time.Second):
        l.handleError(fmt.Errorf("log sync timed out during fatal exit"))
    }
    os.Exit(1)
}

// cleanupRoutine is a goroutine that periodically cleans up old log files.
func (l *Logger) cleanupRoutine() {
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
            l.Sync()
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
        l.handleError(fmt.Errorf("failed to get log file list for cleanup: %v", err))
        return
    }

    now := time.Now()
    deleted := 0

    sort.Slice(files, func(i, j int) bool {
        return files[i].ModTime().After(files[j].ModTime())
    })

    for i, file := range files {
        if file.Name() == l.config.FileName+".log" {
            continue
        }

        shouldDelete := false
        if l.config.MaxAge > 0 && now.Sub(file.ModTime()) > l.config.MaxAge {
            shouldDelete = true
        }
        if l.config.MaxBackups > 0 && i >= l.config.MaxBackups {
            shouldDelete = true
        }

        if shouldDelete {
            filePath := filepath.Join(l.config.LogDir, file.Name())
            if err := os.Remove(filePath); err != nil {
                l.handleError(fmt.Errorf("failed to delete old log file %s: %v", filePath, err))
            } else {
                deleted++
            }
        }
    }

    if deleted > 0 {
        l.Debug("cleaned up %d old log files", deleted)
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
        if name == prefix+".log" ||
            (strings.HasPrefix(name, prefix+"_") && (strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz"))) {
            info, err := entry.Info()
            if err != nil {
                continue
            }
            logFiles = append(logFiles, info)
        }
    }
    return logFiles, nil
}

// Close closes the logger, ensuring all buffered logs are written to disk.
func (l *Logger) Close() error {
    close(l.stopChan)
    l.mu.Lock()
    defer l.mu.Unlock()
    return l.close()
}

// close is the internal implementation of closing the logger.
func (l *Logger) close() error {
    if l.out != nil {
        if f, ok := l.out.(io.Closer); ok {
            f.Close()
        }
    }
    if l.file != nil {
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

    // Flush the bufio.Writer part of the multi-writer
    if f, ok := l.out.(*io.MultiWriter); ok {
        for _, w := range getWriters(f) {
            if b, ok := w.(*bufio.Writer); ok {
                if err := b.Flush(); err != nil {
                    return err
                }
            }
        }
    }

    if l.file != nil {
        return l.file.Sync()
    }
    return nil
}

// A helper to get all writers from a MultiWriter, since it's not exported.
// This is a bit of a hack and depends on the internal structure of MultiWriter.
func getWriters(mw io.Writer) []io.Writer {
    if mw, ok := mw.(interface{ Writers() []io.Writer }); ok {
        return mw.Writers()
    }
    return []io.Writer{mw}
}
