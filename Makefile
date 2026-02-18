.PHONY: up down logs run test

up:
	docker-compose up -d

down:
	docker-compose down

logs:
	docker-compose logs -f

run:
	go run cmd/api/main.go

test:
	go test -v ./...
