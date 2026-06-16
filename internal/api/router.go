package api

import "net/http"

// Register monta los endpoints REST y la documentación Swagger en el mux por defecto.
// apiUser/apiPass: si ambos están definidos, solo /api/* exige HTTP Basic Auth.
func Register(h *Handler, apiUser, apiPass string) {
	wrap := func(handler http.HandlerFunc) http.HandlerFunc {
		return withCORS(withBasicAuth(apiUser, apiPass, handler))
	}
	http.HandleFunc("/api/coverage", wrap(h.CoverageLight))
	http.HandleFunc("/api/coverage/full", wrap(h.CoverageFull))
	http.HandleFunc("/api/dispositivos-ap", wrap(h.DispositivosAP))
	http.HandleFunc("/api/torres", wrap(h.Torres))
	http.HandleFunc("/api/snmp/status", wrap(h.SNMPStatus))
	RegisterSwagger()
}
