package scraper

import (
	"fmt"
	"log"

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

	// Usamos chromium en modo headless (ponlo en false si quieres ver cómo se mueve el navegador al desarrollar)
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
	// NOTA: Debes inspeccionar el HTML de TowerCoverage para confirmar los selectores reales ('#Email', '#Password').
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

	// --- PARA LA PRUEBA DE EXITO ---
	page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String("exito_dashboard.png"),
	})

	log.Println("Login exitoso. Sesión guardada en el contexto.")
	return nil
}

// GetTowersData navega a la URL del mapa inyectando latitud y longitud.
func (s *TowerScraper) GetTowersData(lat, lon string) error {
	log.Printf("Consultando cobertura para Lat: %s, Lon: %s...", lat, lon)

	page, err := s.context.NewPage()
	if err != nil {
		return fmt.Errorf("error creando nueva página: %v", err)
	}
	defer page.Close()

	targetURL := fmt.Sprintf("https://www.towercoverage.com/En-US/Dashboard/LinkPathResult/31710?Lat=%s&Lon=%s&cHgt=0", lat, lon)

	// Aumentamos el tiempo de espera de navegación a 60s porque estos cálculos de mapa son lentos
	if _, err = page.Goto(targetURL, playwright.PageGotoOptions{
		Timeout: playwright.Float(60000),
	}); err != nil {
		return fmt.Errorf("error navegando al mapa de cobertura: %v", err)
	}

	log.Println("URL alcanzada, esperando a que el mapa se inicialice...")

	searchBox := page.Locator("input[placeholder*='Address']") // Busca el input que dice "Address or GPS Coords"

	if err := searchBox.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(45000), // 45 segundos de margen
	}); err != nil {
		// Si falla, tomamos captura para ver qué hay en pantalla (ej. un error 404 o sesión expirada)
		page.Screenshot(playwright.PageScreenshotOptions{Path: playwright.String("debug_mapa_failed.png")})
		return fmt.Errorf("la interfaz del mapa no cargó. Revisa debug_mapa_failed.png: %v", err)
	}

	// Espera de cortesía para que los pines (marcadores) terminen de dibujarse
	page.WaitForTimeout(3000)

	log.Println("Mapa cargado con éxito.")
	page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String(fmt.Sprintf("mapa_%s_%s.png", lat, lon)),
	})

	return nil
}

// ExtractCoverageData itera sobre los resultados y extrae la información.
func (s *TowerScraper) ExtractCoverageData(page playwright.Page) ([]models.TowerCoverage, error) {
	var results []models.TowerCoverage

	// 1. Localizar todas las tarjetas de la columna "Link Results"
	// Necesitamos el selector que engloba cada una de esas cajitas de la derecha
	linkCards := page.Locator("TODO_SELECTOR_LINK_CARD")

	count, err := linkCards.Count()
	if err != nil {
		return nil, fmt.Errorf("error contando resultados: %v", err)
	}

	log.Printf("Se encontraron %d torres en los resultados", count)

	for i := 0; i < count; i++ {
		card := linkCards.Nth(i)

		// 2. Hacer clic en la tarjeta para que el panel central se actualice
		if err := card.Click(); err != nil {
			log.Printf("Error haciendo clic en la tarjeta %d: %v", i, err)
			continue
		}

		// Pequeña espera para que las animaciones/datos del panel central se refresquen
		page.WaitForTimeout(1000)

		// 3. Extraer los datos del panel central
		// Necesitaremos los selectores exactos (clases o IDs) de los textos en amarillo/blanco

		towerName, _ := page.Locator("TODO_SELECTOR_TOWER_NAME").TextContent()
		distanceStr, _ := page.Locator("TODO_SELECTOR_DISTANCE").TextContent()
		signal, _ := page.Locator("TODO_SELECTOR_SIGNAL").TextContent()
		status, _ := page.Locator("TODO_SELECTOR_STATUS").TextContent()

		// Aquí posteriormente agregaremos la lógica en Go para parsear "distanceStr"
		// (ej: extraer "13.09" de "21.062km 13.09mi") y filtrar si es <= 6 millas.

		tower := models.TowerCoverage{
			TowerName: towerName,
			Distance:  distanceStr,
			Signal:    signal,
			Status:    status,
		}

		results = append(results, tower)
		log.Printf("Extraído: %+v", tower)
	}

	return results, nil
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
