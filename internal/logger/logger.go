package logger

import (
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

// Logger wraps logrus.Logger with additional functionality
type Logger struct {
	*logrus.Logger
}

// New creates a new logger instance
func New(level, format string) (*Logger, error) {
	log := logrus.New()

	// Set log level
	logLevel, err := logrus.ParseLevel(strings.ToLower(level))
	if err != nil {
		return nil, fmt.Errorf("invalid log level %s: %w", level, err)
	}
	log.SetLevel(logLevel)

	// Set formatter
	switch strings.ToLower(format) {
	case "json":
		log.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	case "text", "":
		log.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	default:
		return nil, fmt.Errorf("invalid log format %s", format)
	}

	// Set output to stdout
	log.SetOutput(os.Stdout)

	return &Logger{Logger: log}, nil
}

// WithFields creates a new logger entry with the given fields
func (l *Logger) WithFields(fields map[string]interface{}) *logrus.Entry {
	return l.Logger.WithFields(fields)
}

// WithError creates a new logger entry with an error field
func (l *Logger) WithError(err error) *logrus.Entry {
	return l.Logger.WithError(err)
}

// WithComponent creates a new logger entry with a component field
func (l *Logger) WithComponent(component string) *logrus.Entry {
	return l.Logger.WithField("component", component)
}

// LogFirewallOperation logs firewall-related operations
func (l *Logger) LogFirewallOperation(operation, rule string, success bool) {
	fields := logrus.Fields{
		"component": "firewall",
		"operation": operation,
		"rule":      rule,
		"success":   success,
	}

	if success {
		l.WithFields(fields).Info("Firewall operation completed")
	} else {
		l.WithFields(fields).Error("Firewall operation failed")
	}
}

// LogAPIRequest logs API request operations
func (l *Logger) LogAPIRequest(endpoint, method string, statusCode int, duration string) {
	fields := logrus.Fields{
		"component":   "api_client",
		"endpoint":    endpoint,
		"method":      method,
		"status_code": statusCode,
		"duration":    duration,
	}

	if statusCode >= 200 && statusCode < 300 {
		l.WithFields(fields).Info("API request successful")
	} else if statusCode >= 400 {
		l.WithFields(fields).Error("API request failed")
	} else {
		l.WithFields(fields).Warn("API request completed with warning")
	}
}

// LogConfigLoad logs configuration loading operations
func (l *Logger) LogConfigLoad(source string, success bool, err error) {
	fields := logrus.Fields{
		"component": "config",
		"source":    source,
		"success":   success,
	}

	if success {
		l.WithFields(fields).Info("Configuration loaded successfully")
	} else {
		l.WithFields(fields).WithError(err).Error("Failed to load configuration")
	}
}

// LogAgentStart logs agent startup
func (l *Logger) LogAgentStart(version, configPath string) {
	l.WithFields(logrus.Fields{
		"component":   "agent",
		"version":     version,
		"config_path": configPath,
	}).Info("Agent starting")
}

// LogAgentStop logs agent shutdown
func (l *Logger) LogAgentStop(reason string) {
	l.WithFields(logrus.Fields{
		"component": "agent",
		"reason":    reason,
	}).Info("Agent stopping")
}

// LogCollectorRun logs collector execution
func (l *Logger) LogCollectorRun(collector string, duration string, success bool, err error) {
	fields := logrus.Fields{
		"component": "collector",
		"collector": collector,
		"duration":  duration,
		"success":   success,
	}

	if success {
		l.WithFields(fields).Info("Collector executed successfully")
	} else {
		l.WithFields(fields).WithError(err).Error("Collector execution failed")
	}
}

// Fatal logs a fatal error and exits
func (l *Logger) Fatal(args ...interface{}) {
	l.Logger.Fatal(args...)
}

// Fatalf logs a formatted fatal error and exits
func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.Logger.Fatalf(format, args...)
}

// FatalWithFields logs a fatal error with fields and exits
func (l *Logger) FatalWithFields(fields logrus.Fields, message string) {
	l.WithFields(fields).Fatal(message)
}