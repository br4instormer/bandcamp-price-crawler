BIN := bin/crawler
COVERAGE_DIR := coverage

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
	mkdir -p $(COVERAGE_DIR)
	go test ./... -race -coverprofile=$(COVERAGE_DIR)/coverage.out -count=1
	go tool cover -func=$(COVERAGE_DIR)/coverage.out
	go tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html

sonar: coverage
	docker run --rm --network host \
		-v $$(pwd):/usr/src \
		--name sonarqube-bandcamp-price-crawler \
		--env-file=.env \
		sonarsource/sonar-scanner-cli

clean:
	rm -rf bin $(COVERAGE_DIR)
