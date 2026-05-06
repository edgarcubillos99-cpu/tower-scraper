package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

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
		mcp.WithDescription("Obtiene torres cercanas y verifica cobertura de APs (trigonometría y SNMP). "+
			"Un punto: lat + lon. Varios puntos: rellena locations_json con un array JSON (string). "+
			"Varias consultas en paralelo. REST para n8n: POST /api/coverage/full con el mismo cuerpo."),
		mcp.WithString("lat", mcp.Description("Latitud cliente (si es un solo punto y no usas locations_json)")),
		mcp.WithString("lon", mcp.Description("Longitud cliente (si es un solo punto y no usas locations_json)")),
		mcp.WithString("locations_json", mcp.Description(
			`Opcional. Array JSON en texto: [{"lat":"18.4","lon":"-66.2"}]. En HTTP preferir el campo "locations" como array nativo.`)),
	)

	mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_ = ctx // cancelación del cliente MCP (futuro)
		coords, err := coordsFromArgsMap(request.GetArguments())
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		if len(coords) == 1 {
			log.Printf("🤖 MCP Request -> Lat: %s, Lon: %s", coords[0].lat, coords[0].lon)
		} else {
			log.Printf("🤖 MCP Request -> %d ubicaciones en paralelo", len(coords))
		}

		resultJSON, err := marshalCoberturaConsultas(ts, dbClient, coords)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Fallo consulta cobertura: %v", err)), nil
		}

		if len(coords) == 1 {
			log.Println("✅ Respuesta MCP enviada al agente (1 ubicación).")
		} else {
			log.Printf("✅ Respuesta MCP enviada al agente (%d ubicaciones).", len(coords))
		}
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

		// Endpoint REST: solo torres del mapa (ligero)
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

		// Pipeline completo (BD, trig, SNMP) — útil para n8n con HTTP Request
		http.HandleFunc("/api/coverage/full", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "Método no permitido. Usa POST", http.StatusMethodNotAllowed)
				return
			}

			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "No se pudo leer el cuerpo", http.StatusBadRequest)
				return
			}

			dec := json.NewDecoder(bytes.NewReader(bodyBytes))
			dec.UseNumber()
			var raw any
			if err := dec.Decode(&raw); err != nil {
				http.Error(w, fmt.Sprintf("JSON inválido: %v", err), http.StatusBadRequest)
				return
			}

			coords, err := coordsFromAnyRoot(raw)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			payload, err := marshalCoberturaConsultas(ts, dbClient, coords)
			if err != nil {
				http.Error(w, fmt.Sprintf("Fallo consulta cobertura: %v", err), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
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

type coordPair struct {
	lat, lon string
}

func normalizeCoordArg(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	switch x := v.(type) {
	case string:
		s := strings.TrimSpace(x)
		return s, s != ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case json.Number:
		s := strings.TrimSpace(x.String())
		return s, s != ""
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	default:
		return "", false
	}
}

// coordsFromAnyRoot acepta cuerpo objeto o array raíz (n8n / clientes variados).
func coordsFromAnyRoot(raw any) ([]coordPair, error) {
	switch v := raw.(type) {
	case map[string]any:
		return coordsFromArgsMap(v)
	case []any:
		return extractPairsFromSlice(v)
	default:
		return nil, fmt.Errorf("JSON: usa objeto {\"lat\",\"lon\" o \"locations\"...} o array [{\"lat\",\"lon\"},...]")
	}
}

// coordsFromArgsMap interpreta herramienta MCP y POST /api/coverage/full.
// Prioridad: locations_json > locations > lat + lon.
func coordsFromArgsMap(args map[string]any) ([]coordPair, error) {
	if args == nil {
		return nil, fmt.Errorf("sin argumentos")
	}

	if raw, ok := args["locations_json"]; ok && raw != nil {
		s := strings.TrimSpace(coerceToString(raw))
		if s != "" {
			pairs, err := parseLocationsJSONArrayBytes([]byte(s))
			if err != nil {
				return nil, fmt.Errorf("locations_json: %w", err)
			}
			return pairs, nil
		}
	}

	if raw, ok := args["locations"]; ok && raw != nil {
		pairs, err := parseLocationsField(raw)
		if err != nil {
			return nil, fmt.Errorf("locations: %w", err)
		}
		return pairs, nil
	}

	lat, latOk := normalizeCoordArg(args["lat"])
	lon, lonOk := normalizeCoordArg(args["lon"])
	if !latOk || !lonOk {
		return nil, fmt.Errorf("indica lat y lon, o locations (array o string JSON), o locations_json (texto con array JSON)")
	}
	return []coordPair{{lat: lat, lon: lon}}, nil
}

func mapsToCoordPairs(arr []map[string]any) []coordPair {
	var pairs []coordPair
	for _, m := range arr {
		lat, latOk := normalizeCoordArg(m["lat"])
		lon, lonOk := normalizeCoordArg(m["lon"])
		if latOk && lonOk {
			pairs = append(pairs, coordPair{lat: lat, lon: lon})
		}
	}
	return pairs
}

func extractPairsFromSlice(arr []any) ([]coordPair, error) {
	var ms []map[string]any
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ms = append(ms, m)
	}
	pairs := mapsToCoordPairs(ms)
	if len(pairs) == 0 {
		return nil, fmt.Errorf("array sin objetos {{lat, lon}} válidos")
	}
	return pairs, nil
}

// parseLocationsField acepta array nativo, string JSON, u objeto único {lat,lon}.
func parseLocationsField(raw any) ([]coordPair, error) {
	switch v := raw.(type) {
	case []any:
		return extractPairsFromSlice(v)
	case string:
		return parseLocationsJSONArrayBytes([]byte(strings.TrimSpace(v)))
	case map[string]any:
		return extractPairsFromSlice([]any{v})
	default:
		return nil, fmt.Errorf("tipo no soportado (%T); usa array o string con JSON array", raw)
	}
}

// peelJSONStringWrappers quita comillas JSON externas repetidas ("\"[{...}]\"" → contenido interno).
func peelJSONStringWrappers(b []byte) []byte {
	b = bytes.TrimSpace(b)
	for i := 0; i < 6; i++ {
		var inner string
		if err := json.Unmarshal(b, &inner); err != nil {
			break
		}
		next := []byte(strings.TrimSpace(inner))
		if len(next) == 0 || bytes.Equal(b, next) {
			break
		}
		b = next
	}
	return b
}

func tryUnmarshalLocationArray(b []byte) ([]coordPair, bool) {
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil || len(arr) == 0 {
		return nil, false
	}
	p := mapsToCoordPairs(arr)
	if len(p) == 0 {
		return nil, false
	}
	return p, true
}

func tryUnmarshalSingleLocationObject(b []byte) ([]coordPair, bool) {
	var one map[string]any
	if err := json.Unmarshal(b, &one); err != nil {
		return nil, false
	}
	lat, ok1 := normalizeCoordArg(one["lat"])
	lon, ok2 := normalizeCoordArg(one["lon"])
	if !ok1 || !ok2 {
		return nil, false
	}
	return []coordPair{{lat: lat, lon: lon}}, true
}

// parseLocationsJSONArrayBytes tolera string JSON anidado y comillas escapadas típicas de n8n (\" → ").
func parseLocationsJSONArrayBytes(b []byte) ([]coordPair, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, fmt.Errorf("vacío")
	}

	candidates := make([][]byte, 0, 4)
	add := func(x []byte) {
		x = bytes.TrimSpace(x)
		if len(x) == 0 {
			return
		}
		for _, existing := range candidates {
			if bytes.Equal(existing, x) {
				return
			}
		}
		candidates = append(candidates, x)
	}

	add(b)
	add(peelJSONStringWrappers(b))

	sb := string(peelJSONStringWrappers(b))
	if strings.Contains(sb, `\"`) {
		add([]byte(strings.ReplaceAll(sb, `\"`, `"`)))
	}

	for _, cand := range candidates {
		if pairs, ok := tryUnmarshalLocationArray(cand); ok {
			return pairs, nil
		}
	}
	for _, cand := range candidates {
		if pairs, ok := tryUnmarshalSingleLocationObject(cand); ok {
			return pairs, nil
		}
	}

	return nil, fmt.Errorf("no es un array [{lat,lon},...] ni un objeto {{lat,lon}}; revisa comillas y escapes")
}

func coerceToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

type consultaBloque struct {
	Lat        string                 `json:"lat"`
	Lon        string                 `json:"lon"`
	Resultados []models.RespuestaMCP  `json:"resultados"`
	Error      string                 `json:"error,omitempty"`
}

func marshalCoberturaConsultas(ts *scraper.TowerScraper, dbClient *db.DBClient, coords []coordPair) ([]byte, error) {
	if len(coords) == 0 {
		return nil, fmt.Errorf("sin coordenadas")
	}
	if len(coords) == 1 {
		res, err := ejecutarCoberturaParaCoordenada(ts, dbClient, coords[0].lat, coords[0].lon)
		if err != nil {
			return nil, err
		}
		return json.MarshalIndent(res, "", "  ")
	}

	out := make([]consultaBloque, len(coords))
	var wg sync.WaitGroup
	for i, c := range coords {
		wg.Add(1)
		go func(i int, lat, lon string) {
			defer wg.Done()
			res, err := ejecutarCoberturaParaCoordenada(ts, dbClient, lat, lon)
			out[i].Lat, out[i].Lon = lat, lon
			if err != nil {
				out[i].Error = err.Error()
				return
			}
			out[i].Resultados = res
		}(i, c.lat, c.lon)
	}
	wg.Wait()

	wrapped := struct {
		Consultas []consultaBloque `json:"consultas"`
	}{Consultas: out}
	return json.MarshalIndent(wrapped, "", "  ")
}

func ejecutarCoberturaParaCoordenada(ts *scraper.TowerScraper, dbClient *db.DBClient, lat, lon string) ([]models.RespuestaMCP, error) {
	torres, err := ts.GetTowersData(lat, lon)
	if err != nil {
		return nil, err
	}

	var resultadosFinales []models.RespuestaMCP

	for _, torre := range torres {
		log.Printf("Buscando APs en BD para la torre encontrada: %s", torre.TowerName)

		aps, err := dbClient.ObtenerAPsPorTorre(torre.TowerName)
		if err != nil {
			log.Printf("Error BD con torre %s: %v", torre.TowerName, err)
			continue
		}

		if len(aps) > 0 {
			log.Printf("Se encontraron %d APs en DB para %s. Entrando a verificar...", len(aps), torre.TowerName)

			apsAnalizados, errTest := ts.TestAPCoverage(torre, aps, lat, lon)
			if errTest != nil {
				log.Printf("Fallo en la prueba de cobertura para %s: %v", torre.TowerName, errTest)
			}

			resultadosFinales = append(resultadosFinales, apsAnalizados...)
		} else {
			log.Printf("No hay APs configurados en DB para la torre %s", torre.TowerName)
		}
	}

	return resultadosFinales, nil
}