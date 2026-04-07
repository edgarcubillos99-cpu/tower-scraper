# Usamos una imagen base oficial de Go sobre Debian (necesario para las librerías de Playwright)
FROM golang:1.25-bookworm

# Establecemos el directorio de trabajo
WORKDIR /app

# Copiamos los archivos de gestión de dependencias
COPY go.mod go.sum ./

# Descargamos las dependencias de Go
RUN go mod download

# Copiamos el resto del código fuente
COPY . .

# Compilamos el binario de la aplicación
RUN go build -o tower-scraper cmd/scraper/main.go

# Instalamos los navegadores de Playwright y todas las dependencias del SO necesarias (--with-deps)
RUN go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps

# Exponemos el puerto definido por defecto
EXPOSE 8080

# Definimos variables de entorno por defecto para el contenedor
ENV MCP_TRANSPORT=sse
ENV APP_PORT=8080

# Comando de ejecución
CMD ["./tower-scraper"]