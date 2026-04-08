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
	"tower-scraper/internal/scraper"
)

func main() {
	log.Println("Cargando configuración...")
	cfg := config.LoadConfig()

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

	// 1. Inicializamos el Servidor MCP
	mcpServer := server.NewMCPServer(
		"TowerCoverageService",
		"1.0.0",
	)

	// 2. Definimos el "contrato" de la herramienta para que el Agente de IA la entienda
	tool := mcp.NewTool("get_tower_coverage",
		mcp.WithDescription("Consulta TowerCoverage para encontrar torres con 'Good Link' a menos de 6 millas. Devuelve distancias, señal, alineación y tilt."),
		mcp.WithString("lat", mcp.Required(), mcp.Description("Latitud de la antena cliente (ej. 18.465500)")),
		mcp.WithString("lon", mcp.Required(), mcp.Description("Longitud de la antena cliente (ej. -66.105700)")),
	)

	// 3. Añadimos el manejador (Handler) de la herramienta
	mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		lat, err := request.RequireString("lat")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("lat: %v", err)), nil
		}
		lon, err := request.RequireString("lon")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("lon: %v", err)), nil
		}

		log.Printf("🤖 MCP Request recibida -> Lat: %s, Lon: %s", lat, lon)

		// Ejecutamos el scraper
		torres, err := ts.GetTowersData(lat, lon)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Fallo al obtener datos del mapa: %v", err)), nil
		}

		// Convertimos el array de structs a JSON formateado
		resultJSON, err := json.MarshalIndent(torres, "", "  ")
		if err != nil {
			return mcp.NewToolResultError("Error formateando respuesta JSON"), nil
		}

		log.Println("✅ Respuesta MCP enviada al agente.")

		// Retornamos el texto a la IA
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
