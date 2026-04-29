package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// loadDotEnv aplica variables desde ficheros típicos. Usa Overload para que valores
// del .env prevalezcan sobre variables vacías (p. ej. DB_PASS="" inyectado por compose
// antiguo o por el shell), lo que en MySQL aparece como "using password: NO".
func loadDotEnv() {
	for _, p := range []string{"/app/.env", ".env"} {
		_ = godotenv.Overload(p)
	}
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

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
	loadDotEnv()

	appPort := getEnvOrDefault("APP_PORT", "8080")
	username := os.Getenv("TOWER_USERNAME")
	password := os.Getenv("TOWER_PASSWORD")
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USER")
	dbPass := firstEnv("DB_PASS", "DB_PASSWORD", "MYSQL_ROOT_PASSWORD")
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
