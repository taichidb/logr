package logr

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestLoggerBasicFunctionality(t *testing.T) {
    // Create temporary directory
    tempDir := "./test_logs"
    defer os.RemoveAll(tempDir)

    config := &Config{
        LogDir:       tempDir,
        FileName:     "test",
        MaxSize:      1024, // 1KB for quick rotation
        MaxAge:       time.Hour,
        MaxBackups:   3,
        Level:        DEBUG,
        EnableStdout: false,
    }

    logger, err := NewLogger(config)
    if err != nil {
        t.Fatalf("failed to create logger: %v", err)
    }
    defer logger.Close()

    // Test basic log writing
    logger.Debug("Debug message")
    logger.Info("Info message")
    logger.Warn("Warn message")
    logger.Error("Error message")

    // Check if log file exists
    logPath := filepath.Join(tempDir, "test.log")
    if _, err := os.Stat(logPath); os.IsNotExist(err) {
        t.Errorf("log file does not exist: %s", logPath)
    }

    // Read log file content
    content, err := os.ReadFile(logPath)
    if err != nil {
        t.Fatalf("failed to read log file: %v", err)
    }

    contentStr := string(content)
    if !strings.Contains(contentStr, "Debug message") {
        t.Error("Debug message not found in log file")
    }
    if !strings.Contains(contentStr, "Info message") {
        t.Error("Info message not found in log file")
    }
    if !strings.Contains(contentStr, "Warn message") {
        t.Error("Warn message not found in log file")
    }
    if !strings.Contains(contentStr, "Error message") {
        t.Error("Error message not found in log file")
    }
}

func TestLogRotation(t *testing.T) {
    // Create temporary directory
    tempDir := "./test_logs_rotation"
    defer os.RemoveAll(tempDir)

    config := &Config{
        LogDir:       tempDir,
        FileName:     "rotation_test",
        MaxSize:      100, // 100 bytes for quick rotation
        MaxAge:       time.Hour,
        MaxBackups:   3,
        Level:        INFO,
        EnableStdout: false,
    }

    logger, err := NewLogger(config)
    if err != nil {
        t.Fatalf("failed to create logger: %v", err)
    }
    defer logger.Close()

    // Write enough logs to trigger rotation
    for i := 0; i < 10; i++ {
        logger.Info("This is a long log message for testing log rotation functionality - message number: %d", i)
    }

    // Wait a short time to ensure all writes are complete
    time.Sleep(100 * time.Millisecond)

    // Check if backup files were generated
    files, err := os.ReadDir(tempDir)
    if err != nil {
        t.Fatalf("failed to read directory: %v", err)
    }

    logFileCount := 0
    for _, file := range files {
        if strings.HasPrefix(file.Name(), "rotation_test") && strings.HasSuffix(file.Name(), ".log") {
            logFileCount++
        }
    }

    if logFileCount < 2 {
        t.Errorf("expected at least 2 log files (including current file), got %d", logFileCount)
    }
}

func TestLogLevel(t *testing.T) {
    // Create temporary directory
    tempDir := "./test_logs_level"
    defer os.RemoveAll(tempDir)

    config := &Config{
        LogDir:       tempDir,
        FileName:     "level_test",
        MaxSize:      1024 * 1024,
        MaxAge:       time.Hour,
        MaxBackups:   3,
        Level:        WARN, // Only log WARN level and above
        EnableStdout: false,
    }

    logger, err := NewLogger(config)
    if err != nil {
        t.Fatalf("failed to create logger: %v", err)
    }
    defer logger.Close()

    // Write logs of different levels
    logger.Debug("Debug message - should not appear")
    logger.Info("Info message - should not appear")
    logger.Warn("Warn message - should appear")
    logger.Error("Error message - should appear")

    // Read log file content
    logPath := filepath.Join(tempDir, "level_test.log")
    content, err := os.ReadFile(logPath)
    if err != nil {
        t.Fatalf("failed to read log file: %v", err)
    }

    contentStr := string(content)
    if strings.Contains(contentStr, "Debug message") {
        t.Error("log file should not contain Debug message")
    }
    if strings.Contains(contentStr, "Info message") {
        t.Error("log file should not contain Info message")
    }
    if !strings.Contains(contentStr, "Warn message") {
        t.Error("log file should contain Warn message")
    }
    if !strings.Contains(contentStr, "Error message") {
        t.Error("log file should contain Error message")
    }
}

func TestLogCleanup(t *testing.T) {
    // Create temporary directory
    tempDir := "./test_logs_cleanup"
    defer os.RemoveAll(tempDir)

    config := &Config{
        LogDir:       tempDir,
        FileName:     "cleanup_test",
        MaxSize:      50,               // 50 bytes for quick rotation
        MaxAge:       time.Millisecond, // Very short retention time
        MaxBackups:   2,
        Level:        INFO,
        EnableStdout: false,
    }

    logger, err := NewLogger(config)
    if err != nil {
        t.Fatalf("failed to create logger: %v", err)
    }
    defer logger.Close()

    // Write enough logs to generate multiple backup files
    for i := 0; i < 20; i++ {
        logger.Info("Log message %d - for testing cleanup functionality", i)
        time.Sleep(time.Millisecond) // Ensure files have different timestamps
    }

    // Wait for cleanup goroutine to run
    time.Sleep(100 * time.Millisecond)

    // Manually trigger cleanup
    logger.cleanup()

    // Check file count
    files, err := os.ReadDir(tempDir)
    if err != nil {
        t.Fatalf("failed to read directory: %v", err)
    }

    logFileCount := 0
    for _, file := range files {
        if strings.HasPrefix(file.Name(), "cleanup_test") && strings.HasSuffix(file.Name(), ".log") {
            logFileCount++
        }
    }

    // Should only keep current file + MaxBackups backup files
    expectedMaxFiles := config.MaxBackups + 1
    if logFileCount > expectedMaxFiles {
        t.Errorf("expected at most %d log files, got %d", expectedMaxFiles, logFileCount)
    }
}

func TestLogCompressionRotation(t *testing.T) {
    // Create temporary directory
    tempDir := "./test_logs_compression"
    defer os.RemoveAll(tempDir)

    config := &Config{
        LogDir:       tempDir,
        FileName:     "compression_test",
        MaxSize:      100, // 100 bytes for quick rotation
        MaxAge:       time.Hour,
        MaxBackups:   3,
        Level:        INFO,
        EnableStdout: false,
        Compress:     true, // Enable compression
    }

    logger, err := NewLogger(config)
    if err != nil {
        t.Fatalf("failed to create logger: %v", err)
    }
    defer logger.Close()

    // Write enough logs to trigger rotation
    for i := 0; i < 10; i++ {
        logger.Info("This is a long log message for testing log compression functionality - message number: %d", i)
    }

    // Wait a short time to ensure all writes are complete
    time.Sleep(100 * time.Millisecond)

    // Check if compressed backup files were generated
    files, err := os.ReadDir(tempDir)
    if err != nil {
        t.Fatalf("failed to read directory: %v", err)
    }

    logFileCount := 0
    gzFileCount := 0
    for _, file := range files {
        name := file.Name()
        if strings.HasPrefix(name, "compression_test") {
            if strings.HasSuffix(name, ".log") {
                logFileCount++
            } else if strings.HasSuffix(name, ".log.gz") {
                gzFileCount++
            }
        }
    }

    // Should have at least one current log file and some compressed backup files
    if logFileCount < 1 {
        t.Errorf("expected at least 1 current log file, got %d", logFileCount)
    }
    if gzFileCount < 1 {
        t.Errorf("expected at least 1 compressed backup file, got %d", gzFileCount)
    }

    t.Logf("Found %d log files and %d compressed files", logFileCount, gzFileCount)
}

func BenchmarkLoggerWrite(b *testing.B) {
    // Create temporary directory
    tempDir := "./bench_logs"
    defer os.RemoveAll(tempDir)

    config := &Config{
        LogDir:       tempDir,
        FileName:     "bench",
        MaxSize:      100 * 1024 * 1024, // 100MB
        MaxAge:       time.Hour,
        MaxBackups:   5,
        Level:        INFO,
        EnableStdout: false,
    }

    logger, err := NewLogger(config)
    if err != nil {
        b.Fatalf("failed to create logger: %v", err)
    }
    defer logger.Close()

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            logger.Info("This is a benchmark test log message")
        }
    })
}
