BIN := bin/crawler

.PHONY: build run test race vet coverage sonar clean

build:
	go build -o $(BIN) ./cmd/crawler

run: build
	$(BIN) $(ARGS)

test:
	go test ./... -count=1

race:
	go test ./... -race -count=1

vet:
	go vet ./...

coverage:
	go test ./... -race -coverprofile=coverage.out -count=1
	go tool cover -func=coverage.out

sonar: coverage
	docker run --rm --network host \
		-v $$(pwd):/usr/src \
		--name sonarqube-bandcamp-price-crawler \
		--env-file=.env \
		sonarsource/sonar-scanner-cli

clean:
	rm -rf bin coverage.out
