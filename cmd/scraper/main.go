package main

import (
	"log"

	"tower-scraper/internal/config"
	"tower-scraper/internal/scraper"
	"tower-scraper/internal/storage"
)

func main() {
	// 1. Cargar configuración
	log.Println("Cargando configuración...")
	cfg := config.LoadConfig()

	// 2. Inicializar la conexión a la base de datos
	log.Println("Inicializando base de datos...")
	dbStore, err := storage.NewMySQLStorage(cfg)
	if err != nil {
		log.Fatalf("Error inicializando storage: %v", err)
	}
	defer dbStore.Close()

	// 3. Inicializar el Scraper
	log.Println("Inicializando scraper...")
	ts, err := scraper.NewTowerScraper()
	if err != nil {
		log.Fatalf("Error inicializando scraper: %v", err)
	}
	defer ts.Close()

	// 4. Ejecutar Login
	log.Println("Ejecutando login...")
	err = ts.Login(cfg.Username, cfg.Password)
	if err != nil {
		log.Fatalf("Error en el login: %v", err)
	}

	log.Println("¡Fase 1 completada con éxito! Listo para realizar consultas de mapas.")

	// 5. Fase de Consulta (Variables dinámicas)
	// En el futuro, estas variables podrían venir de un agente de IA o una API
	lat := "18.465500"
	lon := "-66.105700"

	torresCercanas, err := ts.GetTowersData(lat, lon)
	if err != nil {
		log.Fatalf("Fallo al obtener datos del mapa: %v", err)
	}

	log.Printf("Extracción finalizada. %d torres cumplen con el requisito de distancia.", len(torresCercanas))

	// 6. Guardar en Base de Datos
	if len(torresCercanas) > 0 {
		log.Println("Procediendo a guardar los resultados en la base de datos...")
		err = dbStore.SaveTowers(torresCercanas)
		if err != nil {
			log.Printf("Ocurrió un error guardando las torres: %v", err)
		}
	} else {
		log.Println("No hay torres que cumplan el criterio para guardar.")
	}

	log.Println("Proceso de scraping y almacenamiento completado.")
}
