package snmp

import "tower-scraper/internal/models"

const maxClients = 25

// OIDs
const oidCambium = "1.3.6.1.4.1.17713.21.1.2.10.0"
const oidUbiquiti = "1.3.6.1.4.1.41112.1.4.5.1.15.1"

func EvaluateAP(apType string, clients int) models.APStatus {
	status := models.APStatus{
		Type:    apType,
		Clients: clients,
	}

	if clients > maxClients {
		status.IsSaturated = true
		status.Message = "Saturado"
	} else {
		status.IsSaturated = false
		status.Message = "Con espacio"
	}
	return status
}
