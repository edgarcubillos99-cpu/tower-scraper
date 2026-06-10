package scraper

import (
	"fmt"
	"log"
	"strings"

	"github.com/playwright-community/playwright-go"
)

const googleMapsViewportW = 1280
const googleMapsViewportH = 900

// ScreenshotGoogleMaps abre Google Maps en la coordenada indicada y devuelve un PNG en memoria.
func (s *TowerScraper) ScreenshotGoogleMaps(lat, lon string) ([]byte, error) {
	s.pwMu.Lock()
	defer s.pwMu.Unlock()

	lat = strings.TrimSpace(lat)
	lon = strings.TrimSpace(lon)
	if lat == "" || lon == "" {
		return nil, fmt.Errorf("lat y lon son obligatorios")
	}

	log.Printf("Capturando Google Maps para Lat: %s, Lon: %s...", lat, lon)

	ctx, err := s.browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{
			Width:  googleMapsViewportW,
			Height: googleMapsViewportH,
		},
		Locale: playwright.String("es"),
	})
	if err != nil {
		return nil, fmt.Errorf("no se pudo crear contexto para Google Maps: %w", err)
	}
	defer ctx.Close()

	page, err := ctx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir pestaña de Google Maps: %w", err)
	}
	defer page.Close()

	// data=!3m1!1e3 fuerza vista satélite; el marcador queda en lat,lon.
	mapURL := fmt.Sprintf(
		"https://www.google.com/maps/place/%s,%s/@%s,%s,17z/data=!3m1!1e3",
		lat, lon, lat, lon,
	)
	if _, err = page.Goto(mapURL, playwright.PageGotoOptions{
		Timeout: playwright.Float(60000),
	}); err != nil {
		return nil, fmt.Errorf("error navegando a Google Maps: %w", err)
	}

	dismissGoogleConsent(page)

	mapCanvas := page.Locator(`canvas`).First()
	if err := mapCanvas.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(45000),
	}); err != nil {
		return nil, fmt.Errorf("el mapa de Google Maps no cargó a tiempo: %w", err)
	}

	enableGoogleMapsSatellite(page)

	// Las teselas satélite tardan más que el mapa vectorial.
	page.WaitForTimeout(4000)

	png, err := page.Screenshot(playwright.PageScreenshotOptions{
		Type:     playwright.ScreenshotTypePng,
		FullPage: playwright.Bool(false),
	})
	if err != nil {
		return nil, fmt.Errorf("error al capturar pantalla: %w", err)
	}

	log.Printf("Captura de Google Maps lista (%d bytes).", len(png))
	return png, nil
}

func enableGoogleMapsSatellite(page playwright.Page) {
	layerSelectors := []string{
		`button:has-text("Satélite")`,
		`button:has-text("Satellite")`,
		`div[role="menuitemradio"]:has-text("Satélite")`,
		`div[role="menuitemradio"]:has-text("Satellite")`,
		`[aria-label="Satélite"]`,
		`[aria-label="Satellite"]`,
	}
	for _, sel := range layerSelectors {
		btn := page.Locator(sel).First()
		visible, err := btn.IsVisible()
		if err != nil || !visible {
			continue
		}
		if err := btn.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(3000)}); err == nil {
			log.Println("Vista satélite activada desde el menú de capas.")
			page.WaitForTimeout(2000)
			return
		}
	}

	capasBtn := page.Locator(`button:has-text("Capas"), button:has-text("Layers")`).First()
	if visible, _ := capasBtn.IsVisible(); visible {
		if err := capasBtn.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(3000)}); err == nil {
			page.WaitForTimeout(800)
			for _, sel := range layerSelectors {
				btn := page.Locator(sel).First()
				if visible, _ := btn.IsVisible(); visible {
					if err := btn.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(3000)}); err == nil {
						log.Println("Vista satélite activada vía botón Capas.")
						page.WaitForTimeout(2000)
						return
					}
				}
			}
		}
	}
}

func dismissGoogleConsent(page playwright.Page) {
	selectors := []string{
		`button:has-text("Aceptar todo")`,
		`button:has-text("Accept all")`,
		`button:has-text("Rechazar todo")`,
		`button:has-text("Reject all")`,
		`form[action*="consent"] button`,
	}
	for _, sel := range selectors {
		btn := page.Locator(sel).First()
		visible, err := btn.IsVisible()
		if err != nil || !visible {
			continue
		}
		if err := btn.Click(playwright.LocatorClickOptions{Timeout: playwright.Float(3000)}); err == nil {
			log.Println("Diálogo de consentimiento de Google cerrado.")
			page.WaitForTimeout(1500)
			return
		}
	}
}
