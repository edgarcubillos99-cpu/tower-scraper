package vision

import (
	"errors"
	"fmt"

	"gocv.io/x/gocv"
)

// AnalizarCobertura recibe la ruta de la imagen y retorna true si el cliente está en el área azul
func AnalizarCobertura(imagePath string) (bool, error) {
	// 1. Cargar la imagen
	img := gocv.IMRead(imagePath, gocv.IMReadColor)
	if img.Empty() {
		return false, errors.New("no se pudo leer la imagen de captura")
	}
	defer img.Close()

	// 2. Convertir a HSV para independizarnos del brillo
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	// 3. Crear máscaras para aislar los colores
	// Nota: Los rangos HSV en OpenCV son H: 0-180, S: 0-255, V: 0-255

	// Rango para el VERDE (Punto del cliente)
	lowerGreen := gocv.NewScalar(40, 50, 50, 0)
	upperGreen := gocv.NewScalar(80, 255, 255, 0)
	maskGreen := gocv.NewMat()
	defer maskGreen.Close()
	gocv.InRangeWithScalar(hsv, lowerGreen, upperGreen, &maskGreen)

	// Rango para el AZUL (Cono de cobertura)
	lowerBlue := gocv.NewScalar(100, 50, 50, 0)
	upperBlue := gocv.NewScalar(140, 255, 255, 0)
	maskBlue := gocv.NewMat()
	defer maskBlue.Close()
	gocv.InRangeWithScalar(hsv, lowerBlue, upperBlue, &maskBlue)

	// 4. Encontrar las coordenadas del punto verde (cliente)
	contours := gocv.FindContours(maskGreen, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	if contours.Size() == 0 {
		return false, fmt.Errorf("no se detectó el punto verde del cliente en la imagen")
	}

	// Asumimos que el contorno más grande o el primero es el cliente
	clienteContorno := contours.At(0)
	rect := gocv.BoundingRect(clienteContorno)

	// Calcular el centro del punto verde
	centroX := rect.Min.X + (rect.Dx() / 2)
	centroY := rect.Min.Y + (rect.Dy() / 2)

	// 5. Verificar si en esas coordenadas la máscara azul tiene señal (> 0)
	// GetUCharAt retorna el valor del píxel en la máscara (0 = negro, 255 = blanco)
	pixelValor := maskBlue.GetUCharAt(centroY, centroX)

	// Si el valor es mayor a 0, significa que el centro del cliente está sobre cobertura azul
	tieneCobertura := pixelValor > 0

	return tieneCobertura, nil
}
