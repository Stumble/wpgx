GO := go
NAME := wpgx
CGO_ENABLED = 0

POSTGRES_DOCKER_NAME=$(NAME)-postgres
POSTGRES_PASSWORD=my-secret
POSTGRES_DB=$(NAME)_test_db
POSTGRES_PORT=5432
TIMEZONE=America/Los_Angeles

docker-postgres-start:
	docker run -d --name $(POSTGRES_DOCKER_NAME) -e PGTZ=$(TIMEZONE) -e POSTGRES_PASSWORD=$(POSTGRES_PASSWORD) -e POSTGRES_DB=$(POSTGRES_DB) -p $(POSTGRES_PORT):5432 postgres:14.5
	sleep 2

docker-postgres-stop:
	docker stop $(POSTGRES_DOCKER_NAME)
	docker rm $(POSTGRES_DOCKER_NAME)

test-start-all:
	make docker-postgres-start

test-stop-all:
	make docker-postgres-stop

test-cmd:
	export ENV=test && \
	export POSTGRES_APPNAME=wpgx && \
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test -count=1 -p 1 ./... -test.v

test-update-golden-cmd:
	export ENV=test && \
	export POSTGRES_APPNAME=wpgx && \
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test -count=1 -p 1 ./... -test.v -update

# Test with testcontainers (no need for manual docker start/stop)
test-container-cmd:
	export ENV=test && \
	export POSTGRES_APPNAME=wpgx && \
	export WPGX_TEST_USE_CONTAINER=true && \
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test -count=1 -p 1 ./... -test.v

test-container-update-golden-cmd:
	export ENV=test && \
	export POSTGRES_APPNAME=wpgx && \
	export WPGX_TEST_USE_CONTAINER=true && \
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test -count=1 -p 1 ./... -test.v -update

# Original test with manual docker management (direct connection)
test: test-start-all
	make test-cmd && make test-stop-all || (make test-stop-all; exit 2)

# Test with testcontainers (recommended for CI/CD)
test-container:
	make test-container-cmd

.PHONY: lint lint-fix
lint:
	@echo "--> Running linter"
	@golangci-lint run

lint-fix:
	@echo "--> Running linter auto fix"
	@golangci-lint run --fix
