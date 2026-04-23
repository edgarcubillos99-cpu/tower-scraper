package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Username  string
	Password  string
	AppPort   string
	MCPAPIKey string // Si no está vacía, /sse y /message exigen Authorization: Bearer <valor>
	DBHost    string
	DBUser    string
	DBPass    string
	DBName    string
}

func LoadConfig() *Config {
	_ = godotenv.Load()

	appPort := getEnvOrDefault("APP_PORT", "8080")
	username := os.Getenv("TOWER_USERNAME")
	password := os.Getenv("TOWER_PASSWORD")
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USER")
	dbPass := os.Getenv("DB_PASS")
	dbName := os.Getenv("DB_NAME")

	if username == "" || password == "" {
		log.Fatal("Faltan credenciales TOWER_USERNAME o TOWER_PASSWORD en el entorno")
	}

	return &Config{
		Username:  username,
		Password:  password,
		AppPort:   appPort,
		MCPAPIKey: os.Getenv("MCP_API_KEY"),
		DBHost:    dbHost,
		DBUser:    dbUser,
		DBPass:    dbPass,
		DBName:    dbName,
	}
}

// Función auxiliar para mantener limpio el código
func getEnvOrDefault(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
