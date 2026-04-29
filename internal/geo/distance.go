package geo

import "math"

const radioTierraKm = 6371.0

// gradosARadianes convierte grados a radianes
func gradosARadianes(grados float64) float64 {
	return grados * math.Pi / 180
}

// CalcularDistancia devuelve la distancia en kilómetros entre dos coordenadas
func CalcularDistancia(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := gradosARadianes(lat2 - lat1)
	dLon := gradosARadianes(lon2 - lon1)

	lat1Rad := gradosARadianes(lat1)
	lat2Rad := gradosARadianes(lat2)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Sin(dLon/2)*math.Sin(dLon/2)*math.Cos(lat1Rad)*math.Cos(lat2Rad)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return math.Round((radioTierraKm*c)*100) / 100 // Redondeado a 2 decimales
}
