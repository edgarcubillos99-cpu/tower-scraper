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
		mcp.WithDescription("Obtiene torres cercanas y automáticamente verifica la cobertura de sus APs desde TowerCoverage."),
		mcp.WithString("lat", mcp.Required(), mcp.Description("Latitud de la antena cliente (ej. 18.465500)")),
		mcp.WithString("lon", mcp.Required(), mcp.Description("Longitud de la antena cliente (ej. -66.105700)")),
	)

	mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		lat, _ := request.RequireString("lat")
		lon, _ := request.RequireString("lon")

		log.Printf("🤖 MCP Request (n8n) recibida -> Lat: %s, Lon: %s", lat, lon)

		// --- PASO 1: Obtener torres cercanas del mapa ---
		torres, err := ts.GetTowersData(lat, lon)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Fallo al obtener datos del mapa: %v", err)), nil
		}

		// Estructura combinada para devolver al agente
		type TowerResult struct {
			TowerInfo models.TowerCoverage `json:"tower_info"`
			APs       []db.APInfo          `json:"aps_validados,omitempty"`
		}
		var resultadosFinales []TowerResult

		// --- PASOS 2 y 3: Iterar sobre las torres encontradas ---
		for _, torre := range torres {
			log.Printf("Buscando APs en BD para la torre encontrada: %s", torre.TowerName)

			// Consultar la DB con el nombre extraído en el Paso 1
			aps, err := dbClient.ObtenerAPsPorTorre(torre.TowerName)
			if err != nil {
				log.Printf("Error BD con torre %s: %v", torre.TowerName, err)
				resultadosFinales = append(resultadosFinales, TowerResult{TowerInfo: torre})
				continue
			}

			if len(aps) > 0 {
				log.Printf("Se encontraron %d APs en DB para %s. Entrando a verificar...", len(aps), torre.TowerName)

				// Ejecutar navegación (Paso 2, 3 y 4)
				apsValidados, errTest := ts.TestAPCoverage(torre.TowerName, aps, lat, lon)
				if errTest != nil {
					log.Printf("Fallo en la prueba de cobertura para %s: %v", torre.TowerName, errTest)
				}
				resultadosFinales = append(resultadosFinales, TowerResult{
					TowerInfo: torre,
					APs:       apsValidados,
				})
			} else {
				log.Printf("No hay APs configurados en DB para la torre %s", torre.TowerName)
				resultadosFinales = append(resultadosFinales, TowerResult{TowerInfo: torre})
			}
		}

		// Convertir resultados a JSON para devolver directamente (sin almacenar)
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
		// --- MODO REMOTO (HTTP / SSE) ---
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

			log.Printf("🌐 Petición REST recibida -> Lat: %s, Lon: %s", reqBody.Lat, reqBody.Lon)

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
		// --- MODO LOCAL (Stdio) ---
		log.Println("🚀 Servidor MCP iniciado en modo Stdio (Local). Esperando instrucciones...")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Error crítico en el servidor MCP por Stdio: %v", err)
		}
	}
}

// mcpBearerAuth protege el transporte MCP (SSE). Si apiKey está vacío, no aplica comprobación.
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
