package db

import (
	"database/sql"
	"fmt"
	"net"
	"strings"
	"tower-scraper/internal/config"

	"github.com/go-sql-driver/mysql"
)

type APInfo struct {
	APName string `json:"ap_name"`
	Tipo   string `json:"tipo"`
	Azimut string `json:"azimut"`
	Tilt   string `json:"tilt"`
	Altura string `json:"altura"`
	Status string `json:"status,omitempty"` // Para marcar si pasó la prueba de cobertura
}

type DBClient struct {
	conn *sql.DB
}

func mysqlAddr(host string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	return net.JoinHostPort(host, "3306")
}

// NewDBClient inicializa la conexión usando DB_HOST, DB_USER, DB_PASS y DB_NAME del entorno (.env).
func NewDBClient(cfg *config.Config) (*DBClient, error) {
	if cfg.DBHost == "" || cfg.DBUser == "" || cfg.DBName == "" {
		return nil, fmt.Errorf("faltan DB_HOST, DB_USER o DB_NAME en el entorno")
	}
	cnf := mysql.Config{
		User:   cfg.DBUser,
		Passwd: cfg.DBPass,
		Net:    "tcp",
		Addr:   mysqlAddr(cfg.DBHost),
		DBName: cfg.DBName,
	}
	db, err := sql.Open("mysql", cnf.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("error conectando a MySQL: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("error haciendo ping a MySQL: %w", err)
	}
	return &DBClient{conn: db}, nil
}

// ObtenerAPsPorTorre cruza la tabla de torres_ap con ap_info
func (c *DBClient) ObtenerAPsPorTorre(nombreTorreTC string) ([]APInfo, error) {
	nombreLimpio := strings.ReplaceAll(nombreTorreTC, "OSN.", "")
	nombreLimpio = strings.TrimSpace(nombreLimpio)
	query := `
		SELECT a.ap_nombre, a.tipo, a.azimut, a.tilt, a.altura 
		FROM dispositivos_ap 
		WHERE torre_nombre LIKE ?
	`
	// El % permite coincidencias parciales (ej: "OSN.Torre Principal" coincidirá con "Torre Principal")
	searchParam := "%" + nombreLimpio + "%"

	rows, err := c.conn.Query(query, searchParam)
	if err != nil {
		return nil, fmt.Errorf("error consultando APs: %w", err)
	}
	defer rows.Close()

	var aps []APInfo
	for rows.Next() {
		var ap APInfo
		if err := rows.Scan(&ap.APName, &ap.Tipo, &ap.Azimut, &ap.Tilt, &ap.Altura); err != nil {
			continue // Podrías agregar un log de advertencia aquí
		}
		aps = append(aps, ap)
	}
	return aps, nil
}
