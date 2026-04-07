package storage

import (
	"database/sql"
	"fmt"
	"log"

	"tower-scraper/internal/config"
	"tower-scraper/internal/models"

	_ "github.com/go-sql-driver/mysql"
)

type MySQLStorage struct {
	db *sql.DB
}

// NewMySQLStorage crea la conexión a la DB y asegura que la estructura exista
func NewMySQLStorage(cfg *config.Config) (*MySQLStorage, error) {
	// DSN (Data Source Name)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("error abriendo conexión a mysql: %v", err)
	}

	// Verificar conexión real
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("error haciendo ping a la base de datos (verifica credenciales y puerto): %v", err)
	}

	// Auto-migración básica
	if err := createTableIfNotExists(db); err != nil {
		return nil, fmt.Errorf("error creando tabla: %v", err)
	}

	log.Println("Conexión a MySQL establecida correctamente.")
	return &MySQLStorage{db: db}, nil
}

func createTableIfNotExists(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS tower_coverage (
		id INT AUTO_INCREMENT PRIMARY KEY,
		tower_name VARCHAR(255) NOT NULL,
		latitude VARCHAR(50),
		longitude VARCHAR(50),
		alignment VARCHAR(50),
		tilt VARCHAR(50),
		distance VARCHAR(50),
		signal_strength VARCHAR(50),
		status VARCHAR(50),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	_, err := db.Exec(query)
	return err
}

// SaveTowers inserta un batch de torres en la base de datos
func (s *MySQLStorage) SaveTowers(towers []models.TowerCoverage) error {
	if len(towers) == 0 {
		return nil
	}

	query := `INSERT INTO tower_coverage (tower_name, latitude, longitude, alignment, tilt, distance, signal_strength, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	// Preparamos el statement para optimizar la inserción en bucle
	stmt, err := s.db.Prepare(query)
	if err != nil {
		return fmt.Errorf("error preparando consulta de inserción: %v", err)
	}
	defer stmt.Close()

	for _, t := range towers {
		_, err := stmt.Exec(t.TowerName, t.Latitude, t.Longitude, t.Alignment, t.Tilt, t.Distance, t.Signal, t.Status)
		if err != nil {
			log.Printf("⚠️ Error guardando torre %s: %v", t.TowerName, err)
		} else {
			log.Printf("💾 Torre %s guardada en DB exitosamente.", t.TowerName)
		}
	}

	return nil
}

// Close limpia los recursos de la BD
func (s *MySQLStorage) Close() {
	if s.db != nil {
		s.db.Close()
	}
}
