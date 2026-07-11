// Package logutil provides structured logging via zap, replacing bare
// log.Printf calls across the project. All log output is tee'd to both
// stderr and (optionally) the console hub so logs remain visible in the
// web UI.
package logutil

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var logger *zap.Logger

// Init initializes the global logger.
//
//   - development: DebugLevel + colorized output
//   - hubSync: optional web console writer (nil to skip)
//   - logPath: optional rotated log file path (empty to skip); backed by lumberjack
//     with 100 MB rotation, 3 backups, 7-day retention, gzip compression.
func Init(development bool, hubSync zapcore.WriteSyncer, logPath string) {
	level := zapcore.InfoLevel
	if development {
		level = zapcore.DebugLevel
	}

	encoderCfg := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.RFC3339TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	consoleEncoder := zapcore.NewConsoleEncoder(encoderCfg)

	cores := []zapcore.Core{
		zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stderr), level),
	}

	if logPath != "" {
		fileSyncer := zapcore.AddSync(&lumberjack.Logger{
			Filename:   logPath,
			MaxSize:    100, // MB
			MaxBackups: 3,
			MaxAge:     7, // days
			Compress:   true,
		})
		cores = append(cores, zapcore.NewCore(consoleEncoder, fileSyncer, level))
	}

	if hubSync != nil {
		// Web console: plain level (no colors), same encoder otherwise.
		webCfg := encoderCfg
		webCfg.EncodeLevel = zapcore.CapitalLevelEncoder
		cores = append(cores,
			zapcore.NewCore(zapcore.NewConsoleEncoder(webCfg), hubSync, level),
		)
	}

	tee := zapcore.NewTee(cores...)
	logger = zap.New(tee, zap.AddCaller(), zap.AddCallerSkip(1))
}

// L returns the global zap.Logger. If Init hasn't been called it falls
// back to a development logger writing to stderr only.
func L() *zap.Logger {
	if logger == nil {
		cfg := zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
		logger, _ = cfg.Build(zap.AddCaller(), zap.AddCallerSkip(1))
	}
	return logger
}

// S returns a SugaredLogger for printf-style calls.
func S() *zap.SugaredLogger { return L().Sugar() }

// ---- structured fields ----

// Info logs at info level with structured fields.
func Info(msg string, fields ...zap.Field) { L().Info(msg, fields...) }

// Warn logs at warn level.
func Warn(msg string, fields ...zap.Field) { L().Warn(msg, fields...) }

// Error logs at error level.
func Error(msg string, fields ...zap.Field) { L().Error(msg, fields...) }

// Debug logs at debug level.
func Debug(msg string, fields ...zap.Field) { L().Debug(msg, fields...) }

// Fatal logs at fatal level and calls os.Exit(1).
func Fatal(msg string, fields ...zap.Field) { L().Fatal(msg, fields...) }

// ---- printf-style ----

// Infof logs a formatted info message.
func Infof(template string, args ...any) { S().Infof(template, args...) }

// Errorf logs a formatted error.
func Errorf(template string, args ...any) { S().Errorf(template, args...) }

// Fatalf logs a formatted fatal and calls os.Exit(1).
func Fatalf(template string, args ...any) { S().Fatalf(template, args...) }
