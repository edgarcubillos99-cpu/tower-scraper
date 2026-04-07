package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"tower-scraper/internal/config"
	"tower-scraper/internal/scraper"
	"tower-scraper/internal/storage"
)

func main() {
	log.Println("Cargando configuración...")
	cfg := config.LoadConfig()

	log.Println("Inicializando base de datos...")
	dbStore, err := storage.NewMySQLStorage(cfg)
	if err != nil {
		log.Fatalf("Error inicializando storage: %v", err)
	}
	defer dbStore.Close()

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

		// Ejecutamos el scraper (usando la sesión ya abierta)
		torres, err := ts.GetTowersData(lat, lon)
		if err != nil {
			// Devolvemos el error al agente para que sepa que falló
			return mcp.NewToolResultError(fmt.Sprintf("Fallo al obtener datos del mapa: %v", err)), nil
		}

		// Guardado asíncrono: Mientras le respondemos a la IA, guardamos en MySQL en segundo plano
		if len(torres) > 0 {
			go func() {
				if err := dbStore.SaveTowers(torres); err != nil {
					log.Printf("Ocurrió un error guardando las torres en DB: %v", err)
				}
			}()
		}

		// Convertimos el array de structs a JSON formateado para el contexto del LLM
		resultJSON, err := json.MarshalIndent(torres, "", "  ")
		if err != nil {
			return mcp.NewToolResultError("Error formateando respuesta JSON"), nil
		}

		log.Println("✅ Respuesta MCP enviada al agente.")

		// Retornamos el texto a la IA
		return mcp.NewToolResultText(string(resultJSON)), nil
	})

	// 4. Configuración del Transporte (Dual Mode: Stdio o SSE)
	// Leemos la variable de entorno, por defecto será "stdio" para desarrollo local
	mcpTransport := os.Getenv("MCP_TRANSPORT")
	if mcpTransport == "" {
		mcpTransport = "stdio"
	}

	if mcpTransport == "sse" {
		// --- MODO REMOTO (HTTP / SSE) ---
		// Ideal para cuando el Agente de IA está en otro contenedor o servidor
		log.Printf("🚀 Servidor MCP iniciado en modo SSE (Remoto). Escuchando en el puerto %s...", cfg.AppPort)

		sseServer := server.NewSSEServer(mcpServer)

		// Endpoints estándar del protocolo MCP sobre HTTP
		http.Handle("/sse", sseServer.SSEHandler())
		http.Handle("/message", sseServer.MessageHandler())

		// Endpoint REST tradicional (Para Postman, cURL, otras apps)
		http.HandleFunc("/api/coverage", func(w http.ResponseWriter, r *http.Request) {
			// Solo permitimos método POST
			if r.Method != http.MethodPost {
				http.Error(w, "Método no permitido. Usa POST", http.StatusMethodNotAllowed)
				return
			}

			// Estructura para leer el JSON de entrada
			var reqBody struct {
				Lat string `json:"lat"`
				Lon string `json:"lon"`
			}

			// Decodificamos el JSON
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				http.Error(w, "JSON inválido", http.StatusBadRequest)
				return
			}

			log.Printf("🌐 Petición REST recibida -> Lat: %s, Lon: %s", reqBody.Lat, reqBody.Lon)

			// Ejecutamos el scraper
			torres, err := ts.GetTowersData(reqBody.Lat, reqBody.Lon)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error obteniendo datos: %v", err), http.StatusInternalServerError)
				return
			}

			// Guardado asíncrono en DB
			if len(torres) > 0 {
				go func() {
					if err := dbStore.SaveTowers(torres); err != nil {
						log.Printf("Error guardando en DB desde REST: %v", err)
					}
				}()
			}

			// Respondemos con JSON
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(torres)
		})

		// Inyectamos la variable APP_PORT directamente en el listener
		addr := fmt.Sprintf(":%s", cfg.AppPort)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Fatalf("Error crítico en el servidor HTTP/SSE: %v", err)
		}

	} else {
		// --- MODO LOCAL (Stdio) ---
		// Ideal para ejecución con inspectores o agentes locales en la misma máquina
		log.Println("🚀 Servidor MCP iniciado en modo Stdio (Local). Esperando instrucciones...")
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Error crítico en el servidor MCP por Stdio: %v", err)
		}
	}
}
