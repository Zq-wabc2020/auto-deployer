package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// timeFormat is the timestamp prepended to every log line written via Printf.
const timeFormat = "2006-01-02 15:04:05"

// Logger wraps an io.Writer with service-specific prefix.
type Logger struct {
	writer io.Writer
	prefix string
}

// Printf writes formatted output to the logger's writer, prefixed with the
// service name and a timestamp. Raw subprocess output (piped through Write or
// Fprintln) is passed through unmodified -- only tool-generated lines carry
// timestamps.
func (l *Logger) Printf(format string, args ...interface{}) {
	msg := fmt.Sprintf(l.prefix+time.Now().Format(timeFormat)+" "+format+"\n", args...)
	_, _ = l.writer.Write([]byte(msg))
}

// Fprintln writes formatted output to the logger's writer.
func (l *Logger) Fprintln(a ...interface{}) {
	msg := fmt.Sprint(a...) + "\n"
	_, _ = l.writer.Write([]byte(msg))
}

// Write implements io.Writer interface.
func (l *Logger) Write(p []byte) (n int, err error) {
	return l.writer.Write(p)
}

// Manager provides service-specific loggers.
type Manager struct {
	mu      sync.Mutex
	loggers map[string]*Logger
	logDir  string
}

var defaultManager *Manager
var initOnce sync.Once

// Init initializes the global logger manager with the given log directory.
func Init(logDir string) {
	initOnce.Do(func() {
		defaultManager = &Manager{
			loggers: make(map[string]*Logger),
			logDir:  logDir,
		}
	})
}

// GetServiceLogger returns a logger for the given service, writing to the service's log file.
// Auto-initializes with default log directory if Init() was not called.
func GetServiceLogger(serviceName string) *Logger {
	initOnce.Do(func() {
		defaultManager = &Manager{
			loggers: make(map[string]*Logger),
			logDir:  ".deployd/services",
		}
	})
	return defaultManager.GetServiceLogger(serviceName)
}

// GetServiceLogger returns a logger for the given service, writing to the service's log file.
func (m *Manager) GetServiceLogger(serviceName string) *Logger {
	m.mu.Lock()
	defer m.mu.Unlock()

	if l, ok := m.loggers[serviceName]; ok {
		return l
	}

	homeDir, _ := os.UserHomeDir()
	logDir := filepath.Join(homeDir, m.logDir)

	// Auto-create log directory
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return &Logger{writer: os.Stdout, prefix: fmt.Sprintf("[%s] ", serviceName)}
	}

	logPath := filepath.Join(logDir, serviceName+".log")

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// Fallback to stdout if log file cannot be opened
		return &Logger{writer: os.Stdout, prefix: fmt.Sprintf("[%s] ", serviceName)}
	}

	l := &Logger{
		writer: f,
		prefix: fmt.Sprintf("[%s] ", serviceName),
	}
	m.loggers[serviceName] = l
	return l
}

// CloseServiceLogger closes the logger for the given service.
func CloseServiceLogger(serviceName string) {
	defaultManager.mu.Lock()
	defer defaultManager.mu.Unlock()

	if l, ok := defaultManager.loggers[serviceName]; ok {
		if fw, ok := l.writer.(io.Closer); ok {
			_ = fw.Close()
		}
		delete(defaultManager.loggers, serviceName)
	}
}
