FROM ghcr.io/hybridgroup/opencv:4.10.0

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -o /usr/local/bin/tower-scraper cmd/scraper/main.go

# Al no poner @version, Go usa inteligentemente la que esté en tu go.mod
RUN go run github.com/playwright-community/playwright-go/cmd/playwright install --with-deps

EXPOSE 8080
ENV MCP_TRANSPORT=sse
ENV APP_PORT=8080

CMD ["tower-scraper"]
