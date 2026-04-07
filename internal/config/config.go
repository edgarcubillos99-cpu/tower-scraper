package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Username string
	Password string
}

func LoadConfig() *Config {
	// Intentamos cargar el .env, pero si falla no hay problema (puede estar en prod)
	_ = godotenv.Load()

	username := os.Getenv("TOWER_USERNAME")
	password := os.Getenv("TOWER_PASSWORD")

	if username == "" || password == "" {
		log.Fatal("Faltan credenciales TOWER_EMAIL o TOWER_PASSWORD en el entorno")
	}

	return &Config{
		Username: username,
		Password: password,
	}
}
