package main

import (
	"log"

	"tower-scraper/internal/config"
	"tower-scraper/internal/scraper"
)

func main() {
	// 1. Cargar configuración
	log.Println("Cargando configuración...")
	cfg := config.LoadConfig()

	// 2. Inicializar el Scraper
	log.Println("Inicializando scraper...")
	ts, err := scraper.NewTowerScraper()
	if err != nil {
		log.Fatalf("Error inicializando scraper: %v", err)
	}
	defer ts.Close()

	// 3. Ejecutar Login
	log.Println("Ejecutando login...")
	err = ts.Login(cfg.Username, cfg.Password)
	if err != nil {
		log.Fatalf("Error en el login: %v", err)
	}

	log.Println("¡Fase 1 completada con éxito! Listo para realizar consultas de mapas.")

	// 2. Fase de Consulta (Variables dinámicas)
	lat := "18.465500"
	lon := "-66.105700"

	err = ts.GetTowersData(lat, lon)
	if err != nil {
		log.Fatalf("Fallo al obtener datos del mapa: %v", err)
	}

	log.Println("¡Proceso completado! Revisa la captura del mapa generada.")
}
