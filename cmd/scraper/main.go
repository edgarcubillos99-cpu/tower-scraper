package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"tower-scraper/internal/config"
	"tower-scraper/internal/db"
	"tower-scraper/internal/models"
	"tower-scraper/internal/scraper"
)

func main() {
	log.Println("Cargando configuración...")
	cfg := config.LoadConfig()

	// INICIALIZAMOS LA BASE DE DATOS SOLO PARA LECTURA DE APs
	log.Println("Conectando a la base de datos MySQL...")
	dbClient, err := db.NewDBClient(cfg)
	if err != nil {
		log.Fatalf("Error inicializando DB: %v", err)
	}

	log.Println("Inicializando motor Headless...")
	ts, err := scraper.NewTowerScraper()
	if err != nil {
		log.Fatalf("Error inicializando scraper: %v", err)
	}
	defer ts.Close()

	log.Println("Ejecutando login en TowerCoverage...")
	err = ts.Login(cfg.Username, cfg.Password)
	if err != nil {
		log.Fatalf("Error en el login: %v", err)
	}

	mcpServer := server.NewMCPServer("TowerCoverageService", "1.0.0")

	tool := mcp.NewTool("get_tower_coverage",
		mcp.WithDescription("Obtiene torres cercanas y automáticamente verifica la cobertura visual y distancia de sus APs."),
		mcp.WithString("lat", mcp.Required(), mcp.Description("Latitud de la antena cliente (ej. 18.465500)")),
		mcp.WithString("lon", mcp.Required(), mcp.Description("Longitud de la antena cliente (ej. -66.105700)")),
	)

	mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		lat, _ := request.RequireString("lat")
		lon, _ := request.RequireString("lon")

		log.Printf("🤖 MCP Request recibida -> Lat: %s, Lon: %s", lat, lon)

		// --- PASO 1: Obtener torres cercanas del mapa ---
		torres, err := ts.GetTowersData(lat, lon)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Fallo al obtener datos del mapa: %v", err)), nil
		}

		// Array plano con el formato exacto que espera el agente IA
		var resultadosFinales []models.RespuestaMCP

		// --- PASOS 2, 3, 4 y 5: Iterar sobre las torres encontradas ---
		for _, torre := range torres {
			log.Printf("Buscando APs en BD para la torre encontrada: %s", torre.TowerName)

			aps, err := dbClient.ObtenerAPsPorTorre(torre.TowerName)
			if err != nil {
				log.Printf("Error BD con torre %s: %v", torre.TowerName, err)
				continue
			}

			if len(aps) > 0 {
				log.Printf("Se encontraron %d APs en DB para %s. Entrando a verificar...", len(aps), torre.TowerName)

				// TestAPCoverage ahora ejecuta navegación, captura, cálculo geoespacial y visión artificial
				apsAnalizados, errTest := ts.TestAPCoverage(torre, aps, lat, lon)
				if errTest != nil {
					log.Printf("Fallo en la prueba de cobertura para %s: %v", torre.TowerName, errTest)
				}
				
				// Agregamos los resultados de los APs procesados a la lista final
				resultadosFinales = append(resultadosFinales, apsAnalizados...)
			} else {
				log.Printf("No hay APs configurados en DB para la torre %s", torre.TowerName)
			}
		}

		// Convertir resultados a JSON para devolver directamente al agente
		resultJSON, err := json.MarshalIndent(resultadosFinales, "", "  ")
		if err != nil {
			return mcp.NewToolResultError("Error formateando respuesta JSON"), nil
		}

		log.Println("✅ Respuesta MCP enviada al agente.")
		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	// 4. Configuración del Transporte (Dual Mode: Stdio o SSE)
	mcpTransport := os.Getenv("MCP_TRANSPORT")
	if mcpTransport == "" {
		mcpTransport = "stdio"
	}

	if mcpTransport == "sse" {
		log.Printf("🚀 Servidor iniciado en modo SSE (Remoto). Escuchando en el puerto %s...", cfg.AppPort)

		if cfg.MCPAPIKey == "" {
			log.Println("⚠️ MCP_API_KEY no definida: los endpoints /sse y /message aceptan conexiones sin autenticación")
		}

		sseServer := server.NewSSEServer(mcpServer)

		http.Handle("/sse", mcpBearerAuth(cfg.MCPAPIKey, sseServer.SSEHandler()))
		http.Handle("/message", mcpBearerAuth(cfg.MCPAPIKey, sseServer.MessageHandler()))

		// Endpoint REST tradicional
		http.HandleFunc("/api/coverage", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "Método no permitido. Usa POST", http.StatusMethodNotAllowed)
				return
			}

			var reqBody struct {
				Lat string `json:"lat"`
				Lon string `json:"lon"`
			}

			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				http.Error(w, "JSON inválido", http.StatusBadRequest)
				return
			}

			torres, err := ts.GetTowersData(reqBody.Lat, reqBody.Lon)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error obteniendo datos: %v", err), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(torres)
		})

		addr := fmt.Sprintf(":%s", cfg.AppPort)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Fatalf("Error crítico en el servidor HTTP/SSE: %v", err)
		}

	} else {
		log.Println("🚀 Servidor MCP iniciado en modo Stdio (Local). Esperando instrucciones...")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Error crítico en el servidor MCP por Stdio: %v", err)
		}
	}
}

func mcpBearerAuth(apiKey string, next http.Handler) http.Handler {
	if apiKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerTokenMatches(r.Header.Get("Authorization"), apiKey) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="MCP", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerTokenMatches(authHeader, expected string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return false
	}
	token := strings.TrimSpace(authHeader[len(prefix):])
	if token == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}