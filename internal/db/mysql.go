package db

import (
	"database/sql"
	"fmt"
	"net"
	"tower-scraper/internal/config"

	"github.com/go-sql-driver/mysql"
)

type APInfo struct {
	Nombre string
	Tipo   string
	Azimut string
	Tilt   string
	Altura string
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
func (c *DBClient) ObtenerAPsPorTorre(nombreTorre string) ([]APInfo, error) {
	query := `
		SELECT a.ap_nombre, a.tipo, a.azimut, a.tilt, a.altura 
		FROM ap_info a
		INNER JOIN torres_ap t ON a.ap_nombre = t.ap_nombre
		WHERE t.nombre_torre = ?
	`
	rows, err := c.conn.Query(query, nombreTorre)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var aps []APInfo
	for rows.Next() {
		var ap APInfo
		if err := rows.Scan(&ap.Nombre, &ap.Tipo, &ap.Azimut, &ap.Tilt, &ap.Altura); err != nil {
			// Un log detallado aquí sería ideal para monitorear filas corruptas
			continue
		}
		aps = append(aps, ap)
	}
	return aps, nil
}
