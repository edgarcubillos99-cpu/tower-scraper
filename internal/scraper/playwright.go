package scraper

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

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
	log.Printf("Iniciando validación en la torre: %s para %d APs", towerName, len(aps))

	page, err := s.context.NewPage()
	if err != nil {
		log.Printf("[TestAPCoverage] fallo paso abrir página: %v", err)
		return nil, fmt.Errorf("error creando página de validación: %v", err)
	}
	defer page.Close()

	// 1. Ir a la vista de Coberturas principal
	if _, err = page.Goto("https://www.towercoverage.com/En-US/Coverages", playwright.PageGotoOptions{
		Timeout: playwright.Float(60000),
	}); err != nil {
		log.Printf("[TestAPCoverage] fallo paso navegar a Coverages: %v", err)
		return nil, fmt.Errorf("error navegando a Coverages: %v", err)
	}

	searchSelector := "input.tablesorter-filter[data-column='1']"
	searchLocator := page.Locator(searchSelector)

	log.Printf("Buscando la torre en la tabla principal: %s", towerName)

	if err := searchLocator.WaitFor(playwright.LocatorWaitForOptions{
		State: playwright.WaitForSelectorStateVisible,
	}); err != nil {
		log.Printf("[TestAPCoverage] fallo paso esperar filtro de búsqueda de tabla: %v", err)
		return nil, fmt.Errorf("error esperando input de búsqueda en coverages: %w", err)
	}

	if err := searchLocator.Fill(towerName); err != nil {
		log.Printf("[TestAPCoverage] fallo paso escribir nombre de torre en el filtro: %v", err)
		return nil, fmt.Errorf("error escribiendo en input: %w", err)
	}

	if err := searchLocator.Press("Enter"); err != nil {
		log.Printf("[TestAPCoverage] fallo paso Enter en filtro de torre: %v", err)
	}

	page.WaitForTimeout(1500)

	// 3. Buscar la fila correspondiente a la TORRE
	rowLocators := page.Locator("tr[role='row']:not(.tablesorter-filter-row)")
	count, err := rowLocators.Count()
	if err != nil {
		log.Printf("[TestAPCoverage] fallo paso contar filas de la tabla (torre %q): %v", towerName, err)
		return nil, nil
	}
	if count == 0 {
		log.Printf("[TestAPCoverage] fallo paso tabla sin filas de datos para la torre %q", towerName)
		return nil, nil
	}

	var selectedRow playwright.Locator

	for i := 0; i < count; i++ {
		row := rowLocators.Nth(i)

		listNameText, err := row.Locator("td.listName").InnerText()
		if err != nil {
			log.Printf("[TestAPCoverage] fallo al leer columna listName (fila %d, torre buscada %q): %v", i, towerName, err)
			continue
		}

		listNameText = strings.TrimSpace(listNameText)

		if strings.Contains(strings.ToLower(listNameText), strings.ToLower(towerName)) {
			if selectedRow == nil {
				selectedRow = row
			}

			if strings.HasPrefix(strings.ToUpper(listNameText), "OSN.") {
				selectedRow = row
				log.Printf("Coincidencia perfecta de torre encontrada: %s", listNameText)
				break
			}
		}
	}

	if selectedRow == nil {
		log.Printf("[TestAPCoverage] fallo paso localizar fila de la torre %q en la tabla (sin coincidencia)", towerName)
		return nil, nil
	}

	// 4. Hacer clic para ENTRAR a la configuración de la Torre
	editLink := selectedRow.Locator("td.listName a").First()
	if err := editLink.Click(); err != nil {
		log.Printf("[TestAPCoverage] fallo paso clic en enlace de edición de la torre %q: %v", towerName, err)
		return nil, fmt.Errorf("error haciendo clic en la torre %s: %w", towerName, err)
	}

	if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		log.Printf("[TestAPCoverage] fallo paso esperar carga de página tras entrar a la torre %q: %v", towerName, err)
	}

	log.Printf("✅ Hemos ingresado a la configuración de la torre: %s", towerName)

	// ==========================================
	// 5. BUCLE DE PRUEBAS PARA CADA AP
	// ==========================================
	var apsValidados []db.APInfo

	for _, ap := range aps {
		log.Printf("Prueba de AP -> Nombre: %s | Azimut: %s | Tilt: %s", ap.APName, ap.Azimut, ap.Tilt)

		// 1) Ingresar coordenadas del cliente
		coordenadas := fmt.Sprintf("%s, %s", latCliente, lonCliente)
		addressInput := page.Locator("#address")

		if err := addressInput.Fill(coordenadas); err != nil {
			log.Printf("[TestAPCoverage AP=%s] fallo paso rellenar #address (coordenadas cliente): %v", ap.APName, err)
			continue
		}

		searchBtn := page.Locator("input.newbutton[value='Search']")
		if err := searchBtn.Click(); err != nil {
			log.Printf("[TestAPCoverage AP=%s] fallo paso clic en Search tras coordenadas: %v", ap.APName, err)
		}
		page.WaitForTimeout(1500)

		// 2) Radio System: único select con valor predefinido (índice 1)
		radioSystemSelect := page.Locator("#RadioSystemList")
		if _, err := radioSystemSelect.SelectOption(playwright.SelectOptionValues{
			Indexes: &[]int{1},
		}); err != nil {
			log.Printf("[TestAPCoverage AP=%s] fallo paso seleccionar Radio System (#RadioSystemList): %v", ap.APName, err)
		}

		// 3) Altura en pies: solo si viene de la DB (sin valor por defecto)
		alturaInput := page.Locator("#AntennaHeightfeet")
		alturaPies := strings.TrimSpace(ap.Altura)
		if err := alturaInput.Fill(""); err != nil {
			log.Printf("[TestAPCoverage AP=%s] fallo paso limpiar altura en pies (#AntennaHeightfeet): %v", ap.APName, err)
		}
		if alturaPies != "" {
			if err := alturaInput.Fill(alturaPies); err != nil {
				log.Printf("[TestAPCoverage AP=%s] fallo paso rellenar altura en pies: %v", ap.APName, err)
			}
			if err := alturaInput.Blur(); err != nil {
				log.Printf("[TestAPCoverage AP=%s] fallo paso blur en altura en pies: %v", ap.APName, err)
			}
		} else {
			if err := alturaInput.Blur(); err != nil {
				log.Printf("[TestAPCoverage AP=%s] fallo paso blur en altura en pies (campo vacío en BD): %v", ap.APName, err)
			}
		}

		// 4) Azimuth (solo números): solo si hay dato en la DB
		azimuthInput := page.Locator("#Azimuth")
		reNumeros := regexp.MustCompile(`[^\d.]`)
		azimuthLimpio := strings.TrimSpace(reNumeros.ReplaceAllString(ap.Azimut, ""))
		if err := azimuthInput.Fill(""); err != nil {
			log.Printf("[TestAPCoverage AP=%s] fallo paso limpiar Azimuth (#Azimuth): %v", ap.APName, err)
		}
		if azimuthLimpio != "" {
			if err := azimuthInput.Fill(azimuthLimpio); err != nil {
				log.Printf("[TestAPCoverage AP=%s] fallo paso rellenar Azimuth: %v", ap.APName, err)
			}
			if err := azimuthInput.Press("Enter"); err != nil {
				log.Printf("[TestAPCoverage AP=%s] fallo paso Enter en Azimuth: %v", ap.APName, err)
			}
		}

		// 5) Beamwidth Filter
		beamwidthInput := page.Locator("#BeamwidthFilter")
		if err := beamwidthInput.Fill("90"); err != nil {
			log.Printf("[TestAPCoverage AP=%s] fallo paso rellenar Beamwidth (#BeamwidthFilter): %v", ap.APName, err)
		}
		if err := beamwidthInput.Press("Enter"); err != nil {
			log.Printf("[TestAPCoverage AP=%s] fallo paso Enter en Beamwidth: %v", ap.APName, err)
		}

		// ==========================================
		// NUEVA INTEGRACIÓN: TILT Y SITE TRANSMITTER INFO
		// ==========================================

		// 6) Tilt (readonly): solo si hay dato en la DB (sin valor por defecto)
		reTilt := regexp.MustCompile(`[^\d.-]`)
		tiltLimpio := strings.TrimSpace(reTilt.ReplaceAllString(ap.Tilt, ""))
		if tiltLimpio != "" {
			scriptTilt := fmt.Sprintf(`document.getElementById("AntennaDecimalTilt").value = "%s";`, tiltLimpio)
			if _, err := page.Evaluate(scriptTilt); err != nil {
				log.Printf("[TestAPCoverage AP=%s] fallo paso inyectar Tilt (#AntennaDecimalTilt): %v", ap.APName, err)
			}
		} else {
			if _, err := page.Evaluate(`document.getElementById("AntennaDecimalTilt").value = "";`); err != nil {
				log.Printf("[TestAPCoverage AP=%s] fallo paso limpiar Tilt (#AntennaDecimalTilt): %v", ap.APName, err)
			}
		}

		// 8) ACCIONAR EL BOTÓN "BeamWidth"
		beamwidthBtn := page.Locator("#showFilter")
		if err := beamwidthBtn.Click(); err != nil {
			log.Printf("[TestAPCoverage AP=%s] fallo paso clic en botón BeamWidth (#showFilter): %v", ap.APName, err)
		} else {
			log.Printf("✅ Clic exitoso en BeamWidth para el AP: %s", ap.APName)
		}

		// Damos un tiempo para que el script ShowBeam() termine de ejecutarse
		page.WaitForTimeout(2000)

		// ==========================================
		// 9) EVALUAR EL RESULTADO (Análisis Visual o del DOM)
		// ==========================================

		// (AQUÍ VA LA LÓGICA DE EXTRACCIÓN DEL RESULTADO)

		ap.Status = "Pendiente de validación visual"
		apsValidados = append(apsValidados, ap)
	}

	return apsValidados, nil
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
