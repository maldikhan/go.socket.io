package utils

import "log"

const (
	DEBUG = 0
	INFO  = 1
	WARN  = 2
	ERROR = 3
	NONE  = 4
)

type DefaultLogger struct {
	Level int
}

func (l *DefaultLogger) Debugf(format string, v ...any) {
	if l.Level <= DEBUG {
		log.Printf(format, v...)
	}
}

func (l *DefaultLogger) Infof(format string, v ...any) {
	if l.Level <= INFO {
		log.Printf(format, v...)
	}
}

func (l *DefaultLogger) Warnf(format string, v ...any) {
	if l.Level <= WARN {
		log.Printf(format, v...)
	}
}

func (l *DefaultLogger) Errorf(format string, v ...any) {
	if l.Level <= ERROR {
		log.Printf(format, v...)
	}
}
