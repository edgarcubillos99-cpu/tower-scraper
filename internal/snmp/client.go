package snmp

import (
	"fmt"
	"os"
	"strings"
	"time"
	"tower-scraper/internal/models"

	"github.com/gosnmp/gosnmp"
)

// CheckSaturation consulta por SNMP si un AP está saturado
func CheckSaturation(ap models.AccessPoint) (models.APStatus, error) {
	// 1. Validar que tengamos IP
	if ap.IPAddress == "" {
		return models.APStatus{}, fmt.Errorf("el AP %s no tiene IP asignada", ap.APName)
	}

	// 2. Configurar el cliente SNMP
	community := os.Getenv("SNMP_COMMUNITY")
	if community == "" {
		community = "osnsnmpro" // Fallback por defecto
	}

	snmpClient := &gosnmp.GoSNMP{
		Target:    ap.IPAddress,
		Port:      161,
		Community: community,
		Version:   gosnmp.Version2c,
		Timeout:   time.Duration(2) * time.Second,
		Retries:   2,
	}

	err := snmpClient.Connect()
	if err != nil {
		return models.APStatus{}, fmt.Errorf("error conectando a %s: %v", ap.IPAddress, err)
	}
	defer snmpClient.Conn.Close()

	// 3. Obtener el OID según el tipo de AP (OIDs escalares, usar Get, no Walk).
	// En BD suele venir el modelo completo (p. ej. ePMP3000, ePMP4500, ePMP4600L), no solo "ePMP".
	t := strings.ToLower(strings.TrimSpace(ap.Tipo))
	var oid string
	switch {
	case t == "cambium" || strings.HasPrefix(t, "epmp"):
		oid = oidCambium
	case t == "ac" || t == "ubiquiti" || strings.HasPrefix(t, "uap"):
		oid = oidUbiquiti
	default:
		return models.APStatus{}, fmt.Errorf("tipo de antena no soportado para SNMP: %s", ap.Tipo)
	}

	// 4. Ejecutar la consulta
	result, err := snmpClient.Get([]string{oid})
	if err != nil {
		return models.APStatus{}, fmt.Errorf("error en consulta SNMP a %s: %v", ap.IPAddress, err)
	}

	// 5. Procesar el resultado (Integer, Counter32, Gauge32, etc.)
	clientesConectados := 0
	if len(result.Variables) > 0 {
		clientesConectados = intFromSNMPValue(result.Variables[0].Value)
	}

	// 6. Evaluar la saturación
	return EvaluateAP(ap.Tipo, clientesConectados), nil
}

func intFromSNMPValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case uint:
		return int(n)
	case uint32:
		return int(n)
	case uint64:
		return int(n)
	default:
		return 0
	}
}
