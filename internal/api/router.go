package api

import "net/http"

// Register monta los endpoints REST y la documentación Swagger en el mux por defecto.
func Register(h *Handler) {
	http.HandleFunc("/api/coverage", withCORS(h.CoverageLight))
	http.HandleFunc("/api/coverage/full", withCORS(h.CoverageFull))
	http.HandleFunc("/api/dispositivos-ap", withCORS(h.DispositivosAP))
	http.HandleFunc("/api/torres", withCORS(h.Torres))
	http.HandleFunc("/api/snmp/status", withCORS(h.SNMPStatus))
	RegisterSwagger()
}
