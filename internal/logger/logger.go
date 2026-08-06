package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Logger wraps an io.Writer with service-specific prefix.
type Logger struct {
	writer io.Writer
	prefix string
}

// Printf writes formatted output to the logger's writer.
func (l *Logger) Printf(format string, args ...interface{}) {
	msg := fmt.Sprintf(l.prefix+format+"\n", args...)
	_, _ = l.writer.Write([]byte(msg))
}

// Fprintln writes formatted output to the logger's writer.
func (l *Logger) Fprintln(a ...interface{}) {
	msg := fmt.Sprint(a...) + "\n"
	_, _ = l.writer.Write([]byte(msg))
}

// Manager provides service-specific loggers.
type Manager struct {
	mu       sync.Mutex
	loggers  map[string]*Logger
	logDir   string
}

var defaultManager *Manager
var once sync.Once

// Init initializes the global logger manager with the given log directory.
func Init(logDir string) {
	once.Do(func() {
		defaultManager = &Manager{
			loggers: make(map[string]*Logger),
			logDir:  logDir,
		}
	})
}

// GetServiceLogger returns a logger for the given service, writing to the service's log file.
func GetServiceLogger(serviceName string) *Logger {
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
	logPath := filepath.Join(homeDir, m.logDir, serviceName+".log")

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
