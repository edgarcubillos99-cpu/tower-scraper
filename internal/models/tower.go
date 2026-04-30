package models

// TowerCoverage representa los datos que extraeremos de cada torre
type TowerCoverage struct {
	TowerName string
	Latitude  string
	Longitude string
	Alignment string
	Tilt      string
	Distance  string
	Signal    string
	Status    string
}

type RespuestaMCP struct {
	Torre                DatosTorre `json:"torre"`
	Antena               string     `json:"antena"`
	Tipo                 string     `json:"tipo_de_antena"`
	Distancia            float64    `json:"distancia_entre_antena_y_cliente_km"`
	Cobertura            bool       `json:"cliente_con_cobertura"`
	NombreTorre          string     `json:"nombre_torre"`
	ClientesConectados   *int       `json:"clientes_conectados,omitempty"`
}

type DatosTorre struct {
	Align    string  `json:"Align"`
	Tilt     string  `json:"Tilt"`
	Status   string  `json:"Status"`
	Latitud  float64 `json:"latitud"`
	Longitud float64 `json:"longitud"`
}

type APStatus struct {
	APName      string
	Type        string
	Clients     int
	IsSaturated bool
	Message     string
}

// internal/models/ap.go

type AccessPoint struct {
	ID        int
	TowerName string
	APName    string
	Tipo      string // ej. "ubiquiti" o "cambium"
	Azimut    string
	Tilt      string
	Altura    string
	IPAddress string // NUEVO CAMPO
}
