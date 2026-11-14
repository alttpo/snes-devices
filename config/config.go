// Package config abstracts the configuration settings read by the device drivers so that each host application (e.g.
// SNI, o2) can supply them from its own configuration system.
//
// Keys are lowercase snake_case names without any application-specific prefix, e.g. "fxpakpro_read_timeout" or
// "retroarch_hosts". Host implementations decide how keys map to their own sources (config files, environment
// variables with an application prefix, built-in defaults, etc.).
package config

import (
	"strings"
	"sync/atomic"
	"time"
)

// Config provides configuration settings to the device drivers.
type Config interface {
	// IsSet reports whether the key has a value (including a default value).
	IsSet(key string) bool

	GetBool(key string) bool
	GetString(key string) string
	GetDuration(key string) time.Duration

	// Name returns the user-facing name of the setting for log messages, e.g. the environment variable that sets it.
	Name(key string) string
}

type holder struct{ c Config }

var current atomic.Value

func init() {
	current.Store(holder{Empty{}})
}

// Set installs the configuration used by all drivers. Hosts must call it before initializing any driver.
func Set(c Config) {
	if c == nil {
		c = Empty{}
	}
	current.Store(holder{c})
}

// Get returns the configuration installed by Set, or an empty configuration.
func Get() Config {
	return current.Load().(holder).c
}

// Empty is a Config with no settings.
type Empty struct{}

func (Empty) IsSet(string) bool                { return false }
func (Empty) GetBool(string) bool              { return false }
func (Empty) GetString(string) string          { return "" }
func (Empty) GetDuration(string) time.Duration { return 0 }
func (Empty) Name(key string) string           { return key }

// Map is a simple Config backed by a map, useful for tests and simple hosts. Values may be bool, string or
// time.Duration; strings are parsed for GetBool and GetDuration.
type Map map[string]any

func (m Map) IsSet(key string) bool {
	_, ok := m[key]
	return ok
}

func (m Map) GetBool(key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return IsTruthy(v)
	}
	return false
}

func (m Map) GetString(key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case time.Duration:
		return v.String()
	}
	return ""
}

func (m Map) GetDuration(key string) time.Duration {
	switch v := m[key].(type) {
	case time.Duration:
		return v
	case string:
		d, _ := time.ParseDuration(v)
		return d
	}
	return 0
}

func (m Map) Name(key string) string { return key }

// IsTruthy interprets common boolean spellings ("1", "true", "yes", "on").
func IsTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	}
	return false
}
