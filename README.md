# logr

[![GoDoc](https://godoc.org/gopkg.in/taichidb/logr.v1?status.svg)](https://godoc.org/gopkg.in/taichidb/logr.v1)

`logr` is a simple, configurable, and high-performance logging library for Go. It supports log rotation, custom log levels, and automatic cleanup of old log files.

## Features

- **Log Rotation**: Automatically rotates log files based on size.
- **Log Cleanup**: Automatically deletes old log files based on age and number of backups.
- **Configurable Log Levels**: Supports `DEBUG`, `INFO`, `WARN`, `ERROR`, and `FATAL` log levels.
- **Stdout Output**: Can simultaneously write logs to the console.
- **Gzip Compression**: Automatically compresses rotated log files.
- **Periodic Sync**: Periodically flushes logs to disk to ensure data is not lost.

## Installation

```bash
go get gopkg.in/taichidb/logr.v1
```

## Usage

### Basic Example

```go
package main

import (
	"time"
	"gopkg.in/taichidb/logr.v1"
)

func main() {
	// Create a new logger with default configuration
	logger, err := logr.NewLogger(logr.DefaultConfig())
	if err != nil {
		panic(err)
	}
	defer logger.Close()

	// Log messages
	logger.Info("This is an info message")
	logger.Warn("This is a warning message")
	logger.Error("This is an error message")

	// The program will exit after logging a fatal message
	// logger.Fatal("This is a fatal message") 
}
```

### Custom Configuration

```go
package main

import (
	"time"
	"gopkg.in/taichidb/logr.v1"
)

func main() {
	// Custom configuration
	config := &logr.Config{
		LogDir:       "./logs",
		FileName:     "myapp",
		MaxSize:      50 * 1024 * 1024, // 50MB
		MaxAge:       3 * 24 * time.Hour, // 3 days
		MaxBackups:   5,
		Level:        logr.DEBUG,
		EnableStdout: true,
		Compress:     true,
	}

	// Create a new logger with custom configuration
	logger, err := logr.NewLogger(config)
	if err != nil {
		panic(err)
	}
	defer logger.Close()

	// Log messages
	logger.Debug("This is a debug message")
	logger.Info("This is an info message")
}
```

## Configuration Options

- `LogDir`: The directory where log files are stored.
- `FileName`: The prefix for log file names.
- `MaxSize`: The maximum size of a single log file in bytes.
- `MaxAge`: The maximum time to retain old log files.
- `MaxBackups`: The maximum number of old log files to retain.
- `Level`: The logging level.
- `EnableStdout`: If `true`, logs will also be written to standard output.
- `SyncInterval`: The interval for periodically syncing logs to disk.
- `Compress`: If `true`, rotated log files will be compressed with gzip.

## Log Levels

- `DEBUG`
- `INFO`
- `WARN`
- `ERROR`
- `FATAL`

## Contributing

Contributions are welcome! Please feel free to submit a pull request.

## License

This project is licensed under the MIT License.
