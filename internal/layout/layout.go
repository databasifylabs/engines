package layout

import (
	"os"
	"path/filepath"
)

const (
	LogBackups = 3
	LogMaxSize = 10 << 20
)

type Layout struct {
	Home string
	Run  string
}

func FromEnv() Layout {
	return Layout{Home: home(), Run: envOr("DATABASIFY_RUN", "/run")}
}

func home() string {
	if dir := os.Getenv("DATABASIFY_HOME"); dir != "" {
		return dir
	}

	return filepath.Join("/srv", envOr("DATABASIFY_ENGINE", "databasify"))
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func (l Layout) Manifest() string {
	return filepath.Join(l.Home, "conf", "processes.json")
}

func (l Layout) Logs() string {
	return filepath.Join(l.Home, "logs")
}

func (l Layout) LogFile(unit string) string {
	return filepath.Join(l.Logs(), unit+".log")
}

func (l Layout) EnabledState() string {
	return filepath.Join(l.Home, "state", "enabled.json")
}

func (l Layout) Socket() string {
	return filepath.Join(l.Run, "databasify.sock")
}

func (l Layout) Tasks() string {
	return filepath.Join(l.Run, "databasify", "tasks")
}
