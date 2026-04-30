package db

import (
	"database/sql"
	"fmt"
	"net"
	"strings"
	"tower-scraper/internal/config"

	"tower-scraper/internal/models"

	"github.com/go-sql-driver/mysql"
)

type APInfo struct {
	APName    string `json:"ap_name"`
	Tipo      string `json:"tipo"`
	Azimut    string `json:"azimut"`
	Tilt      string `json:"tilt"`
	Altura    string `json:"altura"`
	IPAddress string `json:"ip_address,omitempty"`
	Status    string `json:"status,omitempty"` // Para marcar si pasó la prueba de cobertura
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
		User:                 cfg.DBUser,
		Passwd:               cfg.DBPass,
		Net:                  "tcp",
		Addr:                 mysqlAddr(cfg.DBHost),
		DBName:               cfg.DBName,
		AllowNativePasswords: true,
	}
	db, err := sql.Open("mysql", cnf.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("error conectando a MySQL: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		if cfg.DBPass == "" {
			return nil, fmt.Errorf("ping MySQL falló con DB_PASS vacío (suele verse como 'using password: NO'); revisa .env y docker-compose: %w", err)
		}
		return nil, fmt.Errorf("error haciendo ping a MySQL: %w", err)
	}
	return &DBClient{conn: db}, nil
}

// ObtenerAPsPorTorre cruza la tabla de torres_ap con ap_info
func (c *DBClient) ObtenerAPsPorTorre(nombreTorreTC string) ([]APInfo, error) {
	nombreLimpio := strings.ReplaceAll(nombreTorreTC, "OSN.", "")
	nombreLimpio = strings.TrimSpace(nombreLimpio)
	query := `SELECT a.ap_name, a.azimut, a.tilt, a.altura, a.tipo, a.ip_address
          FROM dispositivos_ap a 
          WHERE a.torre_nombre LIKE ?`

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
		var ip sql.NullString
		if err := rows.Scan(&ap.APName, &ap.Azimut, &ap.Tilt, &ap.Altura, &ap.Tipo, &ip); err != nil {
			return nil, err
		}
		if ip.Valid {
			ap.IPAddress = strings.TrimSpace(ip.String)
		}
		aps = append(aps, ap)
	}
	return aps, nil
}

func GetAPsByTower(db *sql.DB, towerName string) ([]models.AccessPoint, error) {
	query := `
		SELECT id, torre_nombre, ap_name, tipo, azimut, tilt, altura, ip_address 
		FROM dispositivos_ap 
		WHERE torre_nombre = ?`

	rows, err := db.Query(query, towerName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var aps []models.AccessPoint
	for rows.Next() {
		var ap models.AccessPoint
		// Es importante manejar el NULL si alguna IP quedó vacía (sql.NullString)
		var ip sql.NullString

		err := rows.Scan(
			&ap.ID, &ap.TowerName, &ap.APName, &ap.Tipo,
			&ap.Azimut, &ap.Tilt, &ap.Altura, &ip,
		)
		if err != nil {
			return nil, err
		}

		if ip.Valid {
			ap.IPAddress = ip.String
		}

		aps = append(aps, ap)
	}
	return aps, nil
}
