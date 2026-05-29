package geo

import "strings"

// ObtenerApertura devuelve los grados de apertura (beamwidth) según el tipo de antena.
// Antenas Wave/Wabe (cualquier capitalización) usan 30°; el resto 90° por defecto.
func ObtenerApertura(tipo string) float64 {
	tipoNorm := strings.ToLower(strings.TrimSpace(tipo))
	if strings.Contains(tipoNorm, "wave") || strings.Contains(tipoNorm, "wabe") {
		return 30.0
	}
	return 90.0
}
