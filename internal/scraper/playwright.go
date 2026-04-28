package scraper

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"tower-scraper/internal/db"
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
		log.Printf("[NewTowerScraper] fallo al iniciar Playwright: %v", err)
		return nil, fmt.Errorf("no se pudo iniciar Playwright: %v", err)
	}

	// Usamos chromium en modo headless (false para ver el navegador)
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		log.Printf("[NewTowerScraper] fallo al lanzar Chromium: %v", err)
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
		log.Printf("[Login] fallo al crear contexto del navegador: %v", err)
		return err
	}
	s.context = context

	page, err := context.NewPage()
	if err != nil {
		log.Printf("[Login] fallo al abrir pestaña de login: %v", err)
		return err
	}
	defer page.Close() // Cerramos esta pestaña al terminar el login

	// 1. Navegar a la página de login
	loginURL := "https://www.towercoverage.com/Login"
	if _, err = page.Goto(loginURL); err != nil {
		log.Printf("[Login] fallo al navegar a la URL de login: %v", err)
		return fmt.Errorf("error navegando al login: %v", err)
	}

	// 2. Llenar el formulario
	if err := page.Locator("#UserName").Fill(username); err != nil {
		log.Printf("[Login] fallo al rellenar usuario: %v", err)
		return fmt.Errorf("error llenando username: %v", err)
	}
	if err := page.Locator("#Password").Fill(password); err != nil {
		log.Printf("[Login] fallo al rellenar contraseña: %v", err)
		return fmt.Errorf("error llenando password: %v", err)
	}

	// 3. Hacer clic en el botón de login y esperar a que la red se estabilice
	loginBtn := page.Locator(`input[type="submit"][value="Login"]`)
	if err := loginBtn.Click(); err != nil {
		log.Printf("[Login] fallo al hacer clic en el botón Login: %v", err)
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
		log.Printf("[Login] fallo esperando texto \"Sign Out\" (timeout o no visible): %v", err)
		page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String("error_timeout_login.png")})
		return fmt.Errorf("el dashboard no cargó a tiempo tras el login: %v", err)
	}

	// 4. Validar el éxito
	if page.URL() == loginURL {
		log.Printf("[Login] fallo de validación: seguimos en la URL de login (credenciales incorrectas o error del servidor)")
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
		log.Printf("[GetTowersData] fallo al crear página: %v", err)
		return nil, fmt.Errorf("error creando nueva página: %v", err)
	}
	defer page.Close()

	targetURL := fmt.Sprintf("https://www.towercoverage.com/En-US/Dashboard/LinkPathResult/31710?Lat=%s&Lon=%s&cHgt=0", lat, lon)

	// Aumentamos el tiempo de espera de navegación a 60s
	if _, err = page.Goto(targetURL, playwright.PageGotoOptions{
		Timeout: playwright.Float(60000),
	}); err != nil {
		log.Printf("[GetTowersData] fallo al navegar al mapa (Lat=%s Lon=%s): %v", lat, lon, err)
		return nil, fmt.Errorf("error navegando al mapa de cobertura: %v", err)
	}

	log.Println("URL alcanzada, esperando a que el mapa se inicialice...")

	searchBox := page.Locator("input[placeholder*='Address']")

	if err := searchBox.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(45000),
	}); err != nil {
		log.Printf("[GetTowersData] fallo esperando el buscador del mapa (input Address): %v", err)
		page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String("debug_mapa_failed.png")})
		return nil, fmt.Errorf("la interfaz del mapa no cargó. Revisa debug_mapa_failed.png: %v", err)
	}

	page.WaitForTimeout(3000)
	log.Println("Mapa cargado. Iniciando extracción de datos...")
	return s.ExtractCoverageData(page)
}

// ExtractCoverageData itera sobre los resultados, extrae la información y filtra por distancia.
func (s *TowerScraper) ExtractCoverageData(page playwright.Page) ([]models.TowerCoverage, error) {
	var results []models.TowerCoverage

	linkCards := page.Locator("#linkPanel .linkImg")

	count, err := linkCards.Count()
	if err != nil {
		log.Printf("[ExtractCoverageData] fallo al contar tarjetas #linkPanel .linkImg: %v", err)
		return nil, fmt.Errorf("error contando resultados: %v", err)
	}

	log.Printf("Se encontraron %d torres en los resultados. Procesando...", count)

	for i := 0; i < count; i++ {
		card := linkCards.Nth(i)

		if err := card.Click(); err != nil {
			log.Printf("[ExtractCoverageData] fallo al hacer clic en tarjeta %d: %v", i, err)
			continue
		}

		page.WaitForTimeout(1500)

		titleText, err := page.Locator("#linkResult tr.collapsible td").InnerText()
		if err != nil {
			log.Printf("[ExtractCoverageData] fallo al leer título de torre (tarjeta %d): %v", i, err)
		}
		towerName := cleanTowerName(titleText)

		perfRow := page.Locator("#linkResult tr.deetRow").Filter(playwright.LocatorFilterOptions{
			HasText: "PERFORMANCE",
		}).Locator("table tr").First()

		status, err := perfRow.Locator("td").Nth(0).InnerText()
		if err != nil {
			log.Printf("[ExtractCoverageData] fallo al leer PERFORMANCE status (tarjeta %d, torre %q): %v", i, towerName, err)
		}
		signal, err := perfRow.Locator("td").Nth(1).InnerText()
		if err != nil {
			log.Printf("[ExtractCoverageData] fallo al leer PERFORMANCE signal (tarjeta %d, torre %q): %v", i, towerName, err)
		}
		distanceRaw, err := perfRow.Locator("td").Nth(2).InnerText()
		if err != nil {
			log.Printf("[ExtractCoverageData] fallo al leer PERFORMANCE distance (tarjeta %d, torre %q): %v", i, towerName, err)
		}

		clientRow := page.Locator("#linkResult tr.deetRow").Filter(playwright.LocatorFilterOptions{
			HasText: "CLIENT",
		}).Locator("table tr").First()

		alignmentRaw, err := clientRow.Locator("td").Nth(0).InnerText()
		if err != nil {
			log.Printf("[ExtractCoverageData] fallo al leer CLIENT alignment (tarjeta %d, torre %q): %v", i, towerName, err)
		}
		tiltRaw, err := clientRow.Locator("td").Nth(1).InnerText()
		if err != nil {
			log.Printf("[ExtractCoverageData] fallo al leer CLIENT tilt (tarjeta %d, torre %q): %v", i, towerName, err)
		}

		towerRow := page.Locator("#linkResult tr.deetRow").Filter(playwright.LocatorFilterOptions{
			HasText: "TOWER",
		}).Locator("table tr").First()

		locationRaw, err := towerRow.Locator("td").Nth(0).InnerText()
		if err != nil {
			log.Printf("[ExtractCoverageData] fallo al leer TOWER location (tarjeta %d, torre %q): %v", i, towerName, err)
		}
		lat, lon := parseLocation(locationRaw)

		milesFloat := extractMiles(distanceRaw)
		cleanStatus := strings.TrimSpace(status)

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

func cleanTowerName(raw string) string {
	raw = strings.ReplaceAll(raw, "\u00a0", " ")
	parts := strings.Split(raw, "from ")
	if len(parts) > 1 {
		sub := strings.Split(parts[1], " to")
		return strings.TrimSpace(sub[0])
	}
	return strings.TrimSpace(raw)
}

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
	re := regexp.MustCompile(`([\d\.]+)\s*mi`)
	matches := re.FindStringSubmatch(raw)
	if len(matches) > 1 {
		val, _ := strconv.ParseFloat(matches[1], 64)
		return val
	}
	return 0
}

// TestAPCoverage navega a Coverages, busca la torre, entra en ella y simula la configuración de sus APs
func (s *TowerScraper) TestAPCoverage(towerName string, aps []db.APInfo, latCliente, lonCliente string) ([]db.APInfo, error) {
	safeName := strings.ReplaceAll(towerName, " ", "_")
	safeName = strings.ReplaceAll(safeName, "/", "-")

	log.Printf("Iniciando validación en la torre: %s para %d APs", towerName, len(aps))

	page, err := s.context.NewPage()
	if err != nil {
		return nil, fmt.Errorf("error creando página de validación: %v", err)
	}
	// No usamos defer page.Close() aquí para poder cerrarla anticipadamente y liberar RAM

	if _, err = page.Goto("https://www.towercoverage.com/En-US/Coverages", playwright.PageGotoOptions{
		Timeout: playwright.Float(60000),
	}); err != nil {
		return nil, fmt.Errorf("error navegando a Coverages: %v", err)
	}

	searchLocator := page.Locator("input.tablesorter-filter[data-column='1']").First()
	if err := searchLocator.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		return nil, fmt.Errorf("error esperando input de búsqueda en coverages: %w", err)
	}

	if err := searchLocator.Fill(towerName); err != nil {
		return nil, fmt.Errorf("error escribiendo en input: %w", err)
	}
	searchLocator.Press("Enter")
	page.WaitForTimeout(1500)

	rowLocators := page.Locator("tr[role='row']:not(.tablesorter-filter-row)")
	count, err := rowLocators.Count()
	if err != nil || count == 0 {
		return nil, fmt.Errorf("sin filas de datos para la torre %q", towerName)
	}

	var selectedRow playwright.Locator
	for i := 0; i < count; i++ {
		row := rowLocators.Nth(i)
		listNameText, _ := row.Locator("td.listName").InnerText()
		listNameText = strings.TrimSpace(listNameText)

		if strings.Contains(strings.ToLower(listNameText), strings.ToLower(towerName)) {
			selectedRow = row
			break
		}
	}

	if selectedRow == nil {
		return nil, fmt.Errorf("sin coincidencia de texto en tabla para %q", towerName)
	}

	// Extraer la URL directa de la torre
	editLink := selectedRow.Locator("td.listName a").First()
	href, err := editLink.GetAttribute("href")
	if err != nil {
		return nil, fmt.Errorf("no se pudo extraer la URL de la torre: %v", err)
	}
	towerURL := "https://www.towercoverage.com" + href

	// Cerramos la pestaña general, ya no la necesitamos. Cada worker abrirá la suya.
	page.Close()
	log.Printf("URL directa obtenida: %s. Iniciando Worker Pool...", towerURL)

	// BUCLE CONCURRENTE (WORKER POOL)
	var apsValidados []db.APInfo
	var wg sync.WaitGroup
	var mu sync.Mutex // Para proteger el slice de resultados

	// Canal de trabajos
	type Job struct {
		Index int
		AP    db.APInfo
	}
	jobs := make(chan Job, len(aps))

	numWorkers := 10 // Pestañas simultáneas. Ajusta según los recursos de tu CPU/RAM.

	// Iniciar Workers
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				// Procesamos el AP individualmente
				res := s.processSingleAP(workerID, towerURL, safeName, job.AP, latCliente, lonCliente, job.Index)

				// Guardamos el resultado protegidos de la condición de carrera
				mu.Lock()
				apsValidados = append(apsValidados, res)
				mu.Unlock()
			}
		}(w)
	}

	// Encolar todos los trabajos y cerrar el canal
	for i, ap := range aps {
		jobs <- Job{Index: i, AP: ap}
	}
	close(jobs)

	// Esperar a que todas las pestañas concurrentes terminen
	wg.Wait()

	log.Printf("✅ Todos los %d APs fueron validados concurrentemente.", len(apsValidados))
	return apsValidados, nil
}

// processSingleAP abre una pestaña propia, va directo a la torre y prueba un solo AP
func (s *TowerScraper) processSingleAP(workerID int, towerURL, safeName string, ap db.APInfo, latCliente, lonCliente string, i int) db.APInfo {
	log.Printf("[Worker-%d] Iniciando AP [%d] -> Nombre: %s", workerID, i+1, ap.APName)

	page, err := s.context.NewPage()
	if err != nil {
		log.Printf("[Worker-%d] Error creando pestaña para %s: %v", workerID, ap.APName, err)
		ap.Status = "Error de Pestaña Playwright"
		return ap
	}
	defer page.Close() // Cierra esta pestaña automáticamente al terminar

	// Navegación directa a la configuración de la antena
	if _, err := page.Goto(towerURL, playwright.PageGotoOptions{Timeout: playwright.Float(60000)}); err != nil {
		log.Printf("[Worker-%d] Error navegando para %s: %v", workerID, ap.APName, err)
		ap.Status = "Error cargando URL"
		return ap
	}

	// 1) Coordenadas
	page.Locator("#address").Fill(fmt.Sprintf("%s, %s", latCliente, lonCliente))
	page.Locator("input.newbutton[value='Search']").Click()
	page.WaitForTimeout(1000)

	// 2) Parámetros
	page.Locator("#RadioSystemList").SelectOption(playwright.SelectOptionValues{Indexes: &[]int{1}})
	page.WaitForTimeout(500)

	reNumeros := regexp.MustCompile(`[^\d.]`)
	alturaLimpia := reNumeros.ReplaceAllString(ap.Altura, "")
	if alturaLimpia != "" {
		alturaInput := page.Locator("#AntennaHeightfeet")
		if err := alturaInput.WaitFor(playwright.LocatorWaitForOptions{State: playwright.WaitForSelectorStateVisible}); err == nil {
			alturaInput.Clear()
			alturaInput.Fill(alturaLimpia)
			alturaInput.Blur()
		} else {
			page.Evaluate(fmt.Sprintf(`document.getElementById("AntennaHeightfeet").value = "%s";`, alturaLimpia))
		}
	}

	azimuthLimpio := reNumeros.ReplaceAllString(ap.Azimut, "")
	if azimuthLimpio != "" {
		page.Locator("#Azimuth").Fill(azimuthLimpio)
	}

	tiltLimpio := reNumeros.ReplaceAllString(ap.Tilt, "")
	if tiltLimpio != "" {
		page.Evaluate(fmt.Sprintf(`document.getElementById("AntennaDecimalTilt").value = "%s";`, tiltLimpio))
	}

	// 3) Ejecutar
	page.Locator("#showFilter").Click()

	// Le damos 10 segundos para que el servidor pinte el RF en el mapa
	page.WaitForTimeout(10000)

	// ALEJAR EL MAPA (ZOOM OUT)

	// Buscamos botones comunes de Google Maps o Leaflet
	zoomOutBtn := page.Locator(`button[title="Zoom out"], button[aria-label="Zoom out"], .leaflet-control-zoom-out`).First()
	if count, _ := zoomOutBtn.Count(); count > 0 {
		log.Printf("[Worker-%d] Alejando mapa para %s...", workerID, ap.APName)
		for z := 0; z < 10; z++ { // Clic al menos 10 veces para alejar
			zoomOutBtn.Click()
			page.WaitForTimeout(400) // Pausa para que el mapa asimile el click
		}
	}

	// 📸 PANTALLAZO FINAL
	page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(fmt.Sprintf("%s_AP_%d_%s_resultado.png", safeName, i, ap.APName)),
		FullPage: playwright.Bool(true),
	})

	ap.Status = "Validación visual generada"
	return ap
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
