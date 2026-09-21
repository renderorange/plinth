package log

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

var mu sync.Mutex

func Info(msg string, kvs ...string) {
	write("info", msg, kvs...)
}

func Warn(msg string, kvs ...string) {
	write("warn", msg, kvs...)
}

func Error(msg string, kvs ...string) {
	write("error", msg, kvs...)
}

func write(level, msg string, kvs ...string) {
	e := map[string]string{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"level":     level,
		"msg":       msg,
	}
	for i := 0; i+1 < len(kvs); i += 2 {
		e[kvs[i]] = kvs[i+1]
	}
	if len(kvs)%2 == 1 {
		e["!BADKEY"] = kvs[len(kvs)-1]
	}
	mu.Lock()
	defer mu.Unlock()
	json.NewEncoder(os.Stdout).Encode(e)
}
