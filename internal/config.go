package internal

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

var (
	ErrPanicEnvNotSet = errors.New("environment variable not set")
	ErrPanicEnvNotInt = errors.New("environment variable is not an integer")
)

const (
	EnvServerPort       = "VI_SERVER_PORT"
	EnvDatabaseHost     = "VI_DB_HOST"
	EnvDatabasePort     = "VI_DB_PORT"
	EnvDatabaseUser     = "VI_DB_USER"
	EnvDatabasePassword = "VI_DB_PASSWORD"
	EnvDatabaseName     = "VI_DB_NAME"
)

// ServerConfig contains configuration for the HTTP server.
type ServerConfig struct {
	Port     int
	Database *DatabaseConfig
}

// WorkerConfig contains configuration for the worker.
type WorkerConfig struct {
	Database *DatabaseConfig
}

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
}

func mustGetenv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		panic(fmt.Errorf("%w: %q", ErrPanicEnvNotSet, key))
	}
	return value
}

func mustGetenvAtoi(key string) int {
	valueStr := mustGetenv(key)
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		panic(fmt.Errorf("%w: %q", ErrPanicEnvNotInt, key))
	}
	return value
}

func NewServerConfigFromEnv() *ServerConfig {
	return &ServerConfig{
		Port: mustGetenvAtoi(EnvServerPort),
		Database: &DatabaseConfig{
			Host:     mustGetenv(EnvDatabaseHost),
			Port:     mustGetenvAtoi(EnvDatabasePort),
			User:     mustGetenv(EnvDatabaseUser),
			Password: mustGetenv(EnvDatabasePassword),
			Name:     mustGetenv(EnvDatabaseName),
		},
	}
}

func NewWorkerConfigFromEnv() *WorkerConfig {
	return &WorkerConfig{
		Database: &DatabaseConfig{
			Host:     mustGetenv(EnvDatabaseHost),
			Port:     mustGetenvAtoi(EnvDatabasePort),
			User:     mustGetenv(EnvDatabaseUser),
			Password: mustGetenv(EnvDatabasePassword),
			Name:     mustGetenv(EnvDatabaseName),
		},
	}
}