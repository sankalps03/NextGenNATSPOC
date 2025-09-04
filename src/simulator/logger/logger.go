package logger

import (
	"os"
	"strings"
	"time"
)

var (
	CurrentDir, _ = os.Getwd()

	PathSeparator = "/"

	LogDirectory = "logs"

	LogFileFormat = "02-January-2006 15"

	LogFileTimeFormat = "03:04:05.000000 PM"

	SpaceSeparator = " "

	LogFileDateFormat = "02-January-2006"

	NewLineSeparator = "\n"

	LogFile = "@@@-Motadata-Datastore ###.log"
)

type Logger struct {
	component, directory string
}

func NewLogger(component string, directory string) Logger {

	return Logger{component: component, directory: directory}
}

func (logger *Logger) Trace(message string) {

	logger.format(&message, "TRACE")

	logger.write(&message)
}

func (logger *Logger) Debug(message string) {

	logger.format(&message, "DEBUG")

	logger.write(&message)
}

func (logger *Logger) Info(message string) {

	logger.format(&message, "INFO")

	logger.write(&message)

}

func (logger *Logger) LogQueryPlan(message string) {

	logger.format(&message, "Query Plan")

	logger.write(&message)
}

func (logger *Logger) Warn(message string) {

	logger.format(&message, "WARN")

	logger.write(&message)

}

func (logger *Logger) Fatal(message string) {

	logger.format(&message, "FATAL")

	logger.write(&message)

}

func (logger *Logger) Error(message string) {

	logger.format(&message, "ERROR")

	logger.write(&message)

}

func (logger *Logger) format(message *string, level string) {

	currentDate := time.Now().Format(LogFileDateFormat)

	currentTime := time.Now().Format(LogFileTimeFormat)

	*message = currentDate + SpaceSeparator + currentTime + SpaceSeparator + level + " [" + logger.component + "]:" +
		*message + NewLineSeparator

}

func (logger *Logger) write(message *string) {

	logDir := CurrentDir + PathSeparator + LogDirectory + PathSeparator + logger.directory

	_, err := os.Stat(logDir)

	if os.IsNotExist(err) {

		_ = os.MkdirAll(logDir, 0755)

	}

	logFile := logDir + PathSeparator

	timestamp := time.Now().Format(LogFileFormat)

	logFile = logFile + strings.ReplaceAll(LogFile, "@@@", timestamp)

	file, err := os.OpenFile(strings.ReplaceAll(logFile, "###", logger.component), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)

	if err == nil {

		defer func() {

			_ = file.Close()
		}()

		_, _ = file.WriteString(*message)
	}

}
