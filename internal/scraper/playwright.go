package scraper

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"tower-scraper/internal/models"

	"github.com/playwright-community/playwright-go"
)

type TowerScraper struct {
	pw      *playwright.Playwright
	browser playwright.Browser
	context playwright.BrowserContext
}

// NewTowerScraper inicializa Playwright y el navegador.
func NewTowerScraper() (*TowerScraper, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("no se pudo iniciar Playwright: %v", err)
	}

	// Usamos chromium en modo headless (false para ver el navegador)
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("no se pudo lanzar el navegador: %v", err)
	}

	return &TowerScraper{
		pw:      pw,
		browser: browser,
	}, nil
}

// Login maneja la autenticación y guarda la sesión.
func (s *TowerScraper) Login(username, password string) error {
	log.Println("Iniciando proceso de Login...")

	// Creamos un nuevo contexto
	context, err := s.browser.NewContext()
	if err != nil {
		return err
	}
	s.context = context

	page, err := context.NewPage()
	if err != nil {
		return err
	}
	defer page.Close() // Cerramos esta pestaña al terminar el login

	// 1. Navegar a la página de login
	loginURL := "https://www.towercoverage.com/Login"
	if _, err = page.Goto(loginURL); err != nil {
		return fmt.Errorf("error navegando al login: %v", err)
	}

	// 2. Llenar el formulario

	if err := page.Locator("#UserName").Fill(username); err != nil {
		return fmt.Errorf("error llenando username: %v", err)
	}
	if err := page.Locator("#Password").Fill(password); err != nil {
		return fmt.Errorf("error llenando password: %v", err)
	}

	// 3. Hacer clic en el botón de login y esperar a que la red se estabilice
	loginBtn := page.Locator(`input[type="submit"][value="Login"]`)
	if err := loginBtn.Click(); err != nil {
		// Buena práctica: Tomar captura si falla un paso crítico en modo headless
		page.Screenshot(playwright.PageScreenshotOptions{
			Path: playwright.String("error_login_click.png"),
		})
		return fmt.Errorf("error haciendo click en el botón Login: %v", err)
	}

	// Esperamos a que la página cargue completamente después del login
	signOutBtn := page.GetByText("Sign Out", playwright.PageGetByTextOptions{Exact: playwright.Bool(true)})
	if err := signOutBtn.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String("error_timeout_login.png")})
		return fmt.Errorf("el dashboard no cargó a tiempo tras el login: %v", err)
	}

	// 4. Validar el éxito
	// Verificamos si seguimos en la URL de login, lo que indicaría un error de credenciales.
	if page.URL() == loginURL {
		page.Screenshot(playwright.PageScreenshotOptions{
			Path: playwright.String("error_credenciales.png"),
		})
		return fmt.Errorf("login fallido: posibles credenciales incorrectas, seguimos en la pantalla de login")
	}

	log.Println("Login exitoso. Sesión guardada en el contexto.")
	return nil
}

// GetTowersData navega a la URL del mapa inyectando latitud y longitud.
func (s *TowerScraper) GetTowersData(lat, lon string) ([]models.TowerCoverage, error) {
	log.Printf("Consultando cobertura para Lat: %s, Lon: %s...", lat, lon)

	page, err := s.context.NewPage()
	if err != nil {
		return nil, fmt.Errorf("error creando nueva página: %v", err)
	}
	defer page.Close()

	targetURL := fmt.Sprintf("https://www.towercoverage.com/En-US/Dashboard/LinkPathResult/31710?Lat=%s&Lon=%s&cHgt=0", lat, lon)

	// Aumentamos el tiempo de espera de navegación a 60s porque estos cálculos de mapa son lentos
	if _, err = page.Goto(targetURL, playwright.PageGotoOptions{
		Timeout: playwright.Float(60000),
	}); err != nil {
		return nil, fmt.Errorf("error navegando al mapa de cobertura: %v", err)
	}

	log.Println("URL alcanzada, esperando a que el mapa se inicialice...")

	searchBox := page.Locator("input[placeholder*='Address']") // Busca el input que dice "Address or GPS Coords"

	if err := searchBox.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(45000), // 45 segundos de margen
	}); err != nil {
		// Si falla, tomamos captura para ver qué hay en pantalla (ej. un error 404 o sesión expirada)
		page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String("debug_mapa_failed.png")})
		return nil, fmt.Errorf("la interfaz del mapa no cargó. Revisa debug_mapa_failed.png: %v", err)
	}

	// Espera de cortesía para que los pines (marcadores) terminen de dibujarse
	page.WaitForTimeout(3000)

	log.Println("Mapa cargado con éxito.")
	page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String(fmt.Sprintf("mapa_%s_%s.png", lat, lon)),
	})

	log.Println("Mapa cargado. Iniciando extracción de datos...")
	return s.ExtractCoverageData(page)
}

// ExtractCoverageData itera sobre los resultados, extrae la información y filtra por distancia.
func (s *TowerScraper) ExtractCoverageData(page playwright.Page) ([]models.TowerCoverage, error) {
	var results []models.TowerCoverage

	// 1. Localizar todas las imágenes que actúan como tarjetas en la columna "Link Results"
	linkCards := page.Locator("#linkPanel .linkImg")

	count, err := linkCards.Count()
	if err != nil {
		return nil, fmt.Errorf("error contando resultados: %v", err)
	}

	log.Printf("Se encontraron %d torres en los resultados. Procesando...", count)

	for i := 0; i < count; i++ {
		card := linkCards.Nth(i)

		// 2. Hacer clic en la tarjeta para actualizar el panel central
		if err := card.Click(); err != nil {
			log.Printf("Error haciendo clic en la tarjeta %d: %v", i, err)
			continue
		}

		// Breve pausa para permitir que JavaScript renderice los nuevos datos en la tabla
		page.WaitForTimeout(1500)

		// 3. Extraer el nombre de la torre
		titleText, _ := page.Locator("#linkResult tr.collapsible td").InnerText()
		towerName := cleanTowerName(titleText)

		// 4. Ubicar las filas de PERFORMANCE, CLIENT y TOWER usando un filtro robusto por texto
		perfRow := page.Locator("#linkResult tr.deetRow").Filter(playwright.LocatorFilterOptions{
			HasText: "PERFORMANCE",
		}).Locator("table tr").First()

		// Extraer columnas: Status (0), Signal (1), Distance (2)
		status, _ := perfRow.Locator("td").Nth(0).InnerText()
		signal, _ := perfRow.Locator("td").Nth(1).InnerText()
		distanceRaw, _ := perfRow.Locator("td").Nth(2).InnerText()

		// Extraer la fila CLIENT para obtener ALIGNMENT y TILT ---
		clientRow := page.Locator("#linkResult tr.deetRow").Filter(playwright.LocatorFilterOptions{
			HasText: "CLIENT",
		}).Locator("table tr").First()

		alignmentRaw, _ := clientRow.Locator("td").Nth(0).InnerText()
		tiltRaw, _ := clientRow.Locator("td").Nth(1).InnerText()

		// Extraer la fila TOWER para obtener LOCATION
		towerRow := page.Locator("#linkResult tr.deetRow").Filter(playwright.LocatorFilterOptions{
			HasText: "TOWER",
		}).Locator("table tr").First()

		locationRaw, _ := towerRow.Locator("td").Nth(0).InnerText()
		lat, lon := parseLocation(locationRaw)

		// 5. Parsear la distancia para obtener el valor numérico en millas
		milesFloat := extractMiles(distanceRaw)
		cleanStatus := strings.TrimSpace(status)

		// 6. Aplicar la regla de negocio: Máximo 6 millas y Good Link
		if milesFloat > 0 && milesFloat <= 6.0 && cleanStatus == "Good Link" {
			tower := models.TowerCoverage{
				TowerName: towerName,
				Latitude:  lat,
				Longitude: lon,
				Alignment: strings.TrimSpace(alignmentRaw),
				Tilt:      strings.TrimSpace(tiltRaw),
				Distance:  fmt.Sprintf("%.2f mi", milesFloat),
				Signal:    strings.TrimSpace(signal),
				Status:    cleanStatus,
			}
			results = append(results, tower)
			log.Printf("✅ APROBADA: %s | Align: %s, Tilt: %s, Status: %s", towerName, tower.Alignment, tower.Tilt, tower.Status)
		} else {
			log.Printf("❌ DESCARTADA (> 6 mi o Status no ideal): %s (%.2f mi, %s)", towerName, milesFloat, cleanStatus)
		}
	}
	return results, nil
}

// Funciones de ayuda (Helpers) para limpieza de datos

func cleanTowerName(raw string) string {
	raw = strings.ReplaceAll(raw, "\u00a0", " ") // Limpiar espacios indivisibles (nbsp)
	parts := strings.Split(raw, "from ")
	if len(parts) > 1 {
		sub := strings.Split(parts[1], " to")
		return strings.TrimSpace(sub[0])
	}
	return strings.TrimSpace(raw)
}

// parseLocation extrae latitud y longitud como texto desde la celda LOCATION (p. ej. "18.46, -66.10").
func parseLocation(raw string) (lat, lon string) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\u00a0", " "))
	re := regexp.MustCompile(`(-?\d+(?:\.\d+)?)\s*[,;/]\s*(-?\d+(?:\.\d+)?)`)
	if m := re.FindStringSubmatch(raw); len(m) == 3 {
		return m[1], m[2]
	}
	reNums := regexp.MustCompile(`-?\d+(?:\.\d+)?`)
	parts := reNums.FindAllString(raw, 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", ""
}

func extractMiles(raw string) float64 {
	// Busca cualquier número (incluyendo decimales) justo antes de "mi"
	re := regexp.MustCompile(`([\d\.]+)\s*mi`)
	matches := re.FindStringSubmatch(raw)
	if len(matches) > 1 {
		val, _ := strconv.ParseFloat(matches[1], 64)
		return val
	}
	return 0
}

// Close limpia los recursos al terminar la aplicación
func (s *TowerScraper) Close() {
	if s.context != nil {
		s.context.Close()
	}
	if s.browser != nil {
		s.browser.Close()
	}
	if s.pw != nil {
		s.pw.Stop()
	}
}
